package main

import (
	"github.com/reiver/go-oi"
	"go-telnet-mod"
	"golang.org/x/crypto/bcrypt"
	"log"
	"math/rand"
	"strings"
	"time"
)

// Login modes that tell us how to interpret the line of text we just got from
// the user.

const (
	loginUsernameMode = iota
	loginNewUserYNMode
	loginNewPassword1Mode
	loginNewPassword2Mode
	loginRegularPasswordMode
	loginCommandMode
)

//
// Struct for doppelganger goroutine to keep track of its own state (keeping
// track of logged in user).
//

type userInfo struct {
	writer                               telnet.Writer
	userID                               int64
	userName                             string
	doppelgangerID                       int64
	chatChannelID                        int64
	chatChannelName                      string
	chatChannelCallback                  chan messageFromDoppelgangerToChatChannel
	incomingFromChannelMaster            chan messageFromChannelMasterToDoppelganger
	incomingFromChatChannel              chan messageFromChatChannelToDoppelganger
	mode                                 int
	promptNeeded                         bool
	promptLen                            int
	lineBuffer                           []byte
	backspaceBuffer                      []byte
	attemptingUserName                   string
	attemptingUserNewPassword            string
	controlSequence                      []byte
	ctrlSeqPosition                      int
	cursorColumn                         int
	prevUsrByte                          byte
	echoOn                               bool
	telnetGoroutineHasGoneAway           bool
	cantExitBeforeExitMessageFromChannel bool
	cantExitCount                        int
}

func userExists(username string) (bool, error) {
	cmd := "SELECT userid FROM user WHERE username = ?;"
	stmtSelExisting, err := global.db.Prepare(cmd)
	if err != nil {
		return false, err
	}
	rowsExisting, err := stmtSelExisting.Query(username)
	if err != nil {
		return false, err
	}
	defer rowsExisting.Close()
	var userID int64
	userID = 0
	for rowsExisting.Next() {
		err = rowsExisting.Scan(&userID)
		if err != nil {
			return false, err
		}
	}
	return (userID != 0), nil
}

func createUser(username string, password string) error {
	var pwhashBin []byte
	var pwhashStr string
	var err error
	pwhashBin, err = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	pwhashStr = string(pwhashBin)
	tx, err := global.db.Begin()
	if err != nil {
		return err
	}
	cmd := "SELECT userid FROM user WHERE username = ?;"
	stmtSelExisting, err := tx.Prepare(cmd)
	if err != nil {
		tx.Rollback()
		return err
	}
	rowsExisting, err := stmtSelExisting.Query(username)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer rowsExisting.Close()
	var userID int64
	userID = 0
	for rowsExisting.Next() {
		err = rowsExisting.Scan(&userID)
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	if userID == 0 {
		cmd = "INSERT INTO user (username, password) VALUES (?, ?);"
		stmtIns, err := tx.Prepare(cmd)
		if err != nil {
			tx.Rollback()
			return err
		}
		_, err = stmtIns.Exec(username, pwhashStr)
		if err != nil {
			tx.Rollback()
			return err
		}
	} else {
		cmd = "UPDATE user SET username = ?, password = ? WHERE userid = ?;"
		stmtUpd, err := tx.Prepare(cmd)
		_, err = stmtUpd.Exec(username, pwhashStr, userID)
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	err = tx.Commit()
	return err // can be nil
}

func login(username string, password string) (int64, string, error) {
	cmd := "SELECT userid, username, password FROM user WHERE username = ?;"
	stmtSelExisting, err := global.db.Prepare(cmd)
	if err != nil {
		return 0, "", err
	}
	rowsExisting, err := stmtSelExisting.Query(username)
	if err != nil {
		return 0, "", err
	}
	defer rowsExisting.Close()
	var userID int64
	userID = 0
	var hashedPassword string
	for rowsExisting.Next() {
		//
		// We overwrite "username" with the value from the database -- if
		// database SELECT is case-independent, the representation of the
		// username stored in the database is presumed to be the
		// authoritative version of the username.
		//
		err = rowsExisting.Scan(&userID, &username, &hashedPassword)
		if err != nil {
			return 0, "", err
		}
	}
	if userID == 0 {
		//
		// We should have already checked that the user exists, so we really
		// never should end up here.
		//
		return 0, "", nil
	}
	err = bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	if err != nil {
		return 0, "", nil
	}
	return userID, username, nil
}

//
// Boolean return value indicates if the channel already existed. We don't
// consider this an error, because it's "app layer" rather than "database
// layer", but we could handle it with an error code.
//
func createChatchannel(chatChannelName string) (bool, error) {
	tx, err := global.db.Begin()
	if err != nil {
		return false, err
	}
	cmd := "SELECT channelid FROM channel WHERE channelName = ?;"
	stmtSelExisting, err := tx.Prepare(cmd)
	if err != nil {
		tx.Rollback()
		return false, err
	}
	rowsExisting, err := stmtSelExisting.Query(chatChannelName)
	if err != nil {
		tx.Rollback()
		return false, err
	}
	defer rowsExisting.Close()
	var channelID int64
	channelID = 0
	for rowsExisting.Next() {
		err = rowsExisting.Scan(&channelID)
		if err != nil {
			tx.Rollback()
			return false, err
		}
	}
	var alreadyExists bool
	if channelID == 0 {
		alreadyExists = false
		cmd = "INSERT INTO channel (channelname) VALUES (?);"
		stmtIns, err := tx.Prepare(cmd)
		if err != nil {
			tx.Rollback()
			return false, err
		}
		_, err = stmtIns.Exec(chatChannelName)
		if err != nil {
			tx.Rollback()
			return false, err
		}
	} else {
		alreadyExists = true
		//
		// If you create the same channel twice, it just updates the name. If
		// your database is set to do case-independent SELECTs, this could
		// change the case on the name Maybe not the behavior you expect. We
		// should probably do a real "rename" command!
		//
		cmd = "UPDATE channel SET channelname = ? WHERE channelid = ?;"
		stmtUpd, err := tx.Prepare(cmd)
		_, err = stmtUpd.Exec(chatChannelName, channelID)
		if err != nil {
			tx.Rollback()
			return true, err
		}
	}
	err = tx.Commit()
	return alreadyExists, err // can be nil
}

func getChatchannelList() ([]string, error) {
	cmd := "SELECT channelname FROM channel WHERE 1 ORDER BY channelname;"
	stmtSelExisting, err := global.db.Prepare(cmd)
	if err != nil {
		return nil, err
	}
	rowsExisting, err := stmtSelExisting.Query()
	if err != nil {
		return nil, err
	}
	defer rowsExisting.Close()
	chatChanList := make([]string, 0)
	var chatChannelName string
	for rowsExisting.Next() {
		err = rowsExisting.Scan(&chatChannelName)
		if err != nil {
			return nil, err
		}
		chatChanList = append(chatChanList, chatChannelName)
	}
	return chatChanList, nil
}

func backspaceOut(doppelgangerState *userInfo, amountToBackspace int) error {
	//
	// We optimized this so we're not constantly allocating membory
	// for backspaces.
	//
	if len(doppelgangerState.backspaceBuffer) < (amountToBackspace * 3) {
		additional := make([]byte, (amountToBackspace*3)-len(doppelgangerState.backspaceBuffer))
		for ii := 0; ii < (amountToBackspace*3)-len(doppelgangerState.backspaceBuffer); ii += 3 {
			additional[ii] = 8
			additional[ii+1] = 32
			additional[ii+2] = 8
		}
		doppelgangerState.backspaceBuffer = append(doppelgangerState.backspaceBuffer, additional...)
	}
	_, err := oi.LongWrite(doppelgangerState.writer, doppelgangerState.backspaceBuffer[:amountToBackspace*3])
	return err
}

//
// Boolean return value == promptNeeded.
//
func doCommand(doppelgangerState *userInfo, command string) (bool, error) {
	//
	// Find the space and break the command into command + operand.
	//
	operand := ""
	ii := strings.Index(command, " ")
	if ii > 0 {
		operand = trim(command[ii:])
		command = command[:ii]
	}
	//
	// We convert "say", "think", and "sing" into "emote". Everything said
	// is ultimate said with "emote".
	//
	emoteParameter := ""
	switch command {
	case "/say":
		command = "/emote"
		emoteParameter = doppelgangerState.userName + " says, " + `"` + operand + `"`
	case "/think":
		command = "/emote"
		emoteParameter = doppelgangerState.userName + " thinks . o O ( " + operand + " )"
	case "/sing":
		command = "/emote"
		emoteParameter = doppelgangerState.userName + " sings ~ ~ " + operand + " ~ ~"
	case "/emote":
		emoteParameter = doppelgangerState.userName + " " + operand
	}
	//
	// here's where we execute commands!
	//
	switch command {
	case "/create":
		//
		// Remove (optional) prepended "#" if there is one.
		//
		if len(operand) > 1 {
			if operand[0] == '#' {
				operand = trim(operand[1:])
			}
		}
		if operand == "" {
			_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\nPlease specify a channel name to create.\r\n"))
			return true, err // can be nil
		}
		alreadyExisted, err := createChatchannel(operand)
		if err != nil {
			//
			// We can't return an error because that would indicate to the
			// caller that the user has dropped the connection, and it will
			// exit the doppelganger. But if the error occurred in the db
			// layer, the user is still here. So we log the error, send the
			// user a generic message, and try to keep going.
			//
			log.Println(err)
			_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\nA database error has occurred.\r\n"))
			if err != nil {
				return false, err
			}
		} else {
			if alreadyExisted {
				_, err = oi.LongWrite(doppelgangerState.writer, []byte("\r\nChannel already exists.\r\n"))
			} else {
				_, err = oi.LongWrite(doppelgangerState.writer, []byte("\r\nChannel \"#"+operand+"\" created.\r\n"))
			}
			return true, err // err can be nil
		}
	case "/list":
		chatChannelList, err := getChatchannelList()
		if err != nil {
			//
			// We can't return an error because that would indicate to the
			// caller that the user has dropped the connection, and it will exit
			// the doppelganger. But if the error occurred in the db layer, the
			// user is still here. So we log the error, send the user a generic
			// message, and try to keep going.
			//
			log.Println(err)
			_, err = oi.LongWrite(doppelgangerState.writer, []byte("\r\nA database error has occurred.\r\n"))
			if err != nil {
				return false, err
			}
		}
		//
		// User's carriage return was not echoed.
		//
		_, err = oi.LongWrite(doppelgangerState.writer, []byte("\r\n"))
		if err != nil {
			return false, err
		}
		for _, chatChanName := range chatChannelList {
			_, err = oi.LongWrite(doppelgangerState.writer, []byte("#"+chatChanName+"\r\n"))
			if err != nil {
				return false, err
			}
		}
		return true, nil
	case "/join":
		if doppelgangerState.userID == 0 {
			//
			// This should be impossible to happen because we don't let the
			// user type any commands unless they have successfully completed
			// the login. Nonetheless if they do somehow get here, we do the
			// sensible thing.
			//
			_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\nYou have to log in before you can join a channel.\r\n"))
			return true, err // err can be nil
		} else {
			//
			// Now, to make a truly user-friendly system, when the user asks
			// to join a channel when they are already on another channel,
			// we'd query the new channel to see if we can join, exit our
			// current channel if we can, and then join the new channel, all
			// automatically. But, in the interest of getting this program
			// done, we're going to short-cut that and require the user to
			// explicitly exit their current channel before allowing them to
			// join a new one.
			//
			if doppelgangerState.chatChannelID > 0 {
				_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\nYou have to exit your current channel before you can join a new channel.\r\n"))
				return true, err // err can be nil
			}
			var theMessage messageFromDoppelgangerToChannelMaster
			theMessage.userID = doppelgangerState.userID
			theMessage.userName = doppelgangerState.userName
			theMessage.doppelgangerID = doppelgangerState.doppelgangerID
			theMessage.operation = fromDoppelgangerToChannelMasterOpJoin
			theMessage.parameter = operand
			theMessage.doppelgangerCallbackFromChannelMaster = doppelgangerState.incomingFromChannelMaster
			theMessage.doppelgangerCallbackFromChatChannel = doppelgangerState.incomingFromChatChannel
			if global.chanMasterFromDoppelgangerGoChan == nil {
				//
				// Should never happen.
				//
				logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: global.chanMasterFromDoppelgangerGoChan == nil")
				return false, nil // Try and keep server up (kind of laughable if the go channel to the channel master is gone, though)
			}
			//
			// Here try to prevent the "send on closed channel" error that can
			// occur later on. By changing the chat channel ID here to something
			// OTHER than 0, we signal that we need to keep this goroutine (the
			// doppelganger) going until we've joined and exited the channel.
			// If we left it 0, then a disconnect from the user would cause the
			// channel to be closed and this goroutine to exit, and then AFTER
			// that the chat channel goroutine would receive the join request,
			// and try to send the response on the closed channel, causing the
			// "send on closed channel"
			//
			doppelgangerState.chatChannelID = -1
			global.chanMasterFromDoppelgangerGoChan <- theMessage
			return false, nil
		}
	case "/who":
		if doppelgangerState.userID == 0 {
			//
			// This should be impossible to happen because we don't let the
			// user type any commands unless they have successfully completed
			// the login. Nonetheless if they do somehow get here, we do the
			// sensible thing.
			//
			_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\nYou are not logged in.\r\n"))
			return true, err // err can be nil
		} else {
			if doppelgangerState.chatChannelID == 0 {
				_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\nYou are not on a channel.\r\n"))
				return true, err // err can be nil
			} else {
				_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\n"))
				if err != nil {
					return true, err
				}
				var theMessage messageFromDoppelgangerToChannelMaster
				theMessage.userID = doppelgangerState.userID
				theMessage.userName = doppelgangerState.userName
				theMessage.chatChannelID = doppelgangerState.chatChannelID
				theMessage.operation = fromDoppelgangerToChannelMasterOpWho
				theMessage.parameter = operand
				theMessage.doppelgangerCallbackFromChannelMaster = doppelgangerState.incomingFromChannelMaster
				theMessage.doppelgangerCallbackFromChatChannel = doppelgangerState.incomingFromChatChannel
				if global.chanMasterFromDoppelgangerGoChan == nil {
					//
					// Should never happen.
					//
					logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: global.chanMasterFromDoppelgangerGoChan == nil")
					return false, nil // Try and keep server up (kind of laughable if the go channel to the channel master is gone, though)
				}
				global.chanMasterFromDoppelgangerGoChan <- theMessage
				return true, nil
			}
		}
	case "/exit":
		if doppelgangerState.userID == 0 {
			//
			// This should be impossible to happen because we don't let the
			// user type any commands unless they have successfully completed
			// the login. Nonetheless if they do somehow get here, we do the
			// sensible thing.
			//
			_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\nYou are not logged in.\r\n"))
			return true, err // err can be nil
		} else {
			if doppelgangerState.chatChannelID == 0 {
				_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\nYou are not on a channel. You have to join a channel before you can exit it.\r\n"))
				return true, err // err can be nil
			} else {
				_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\n"))
				if err != nil {
					return true, err
				}
				var theMessage messageFromDoppelgangerToChannelMaster
				theMessage.operation = fromDoppelgangerToChannelMasterOpExit
				theMessage.userID = doppelgangerState.userID
				theMessage.userName = doppelgangerState.userName
				theMessage.doppelgangerID = doppelgangerState.doppelgangerID
				theMessage.chatChannelID = doppelgangerState.chatChannelID
				theMessage.parameter = operand // we could say the name of the channel we're leaving, but it'll be ignored so don't bother
				theMessage.doppelgangerCallbackFromChannelMaster = doppelgangerState.incomingFromChannelMaster
				theMessage.doppelgangerCallbackFromChatChannel = doppelgangerState.incomingFromChatChannel
				if global.chanMasterFromDoppelgangerGoChan == nil {
					//
					// Should never happen.
					//
					logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: global.chanMasterFromDoppelgangerGoChan == nil")
					return false, nil // Try and keep server up (kind of laughable if the go channel to the channel master is gone, though)
				}
				global.chanMasterFromDoppelgangerGoChan <- theMessage
				//
				// We go ahead and set our chat channel to 0 to pre-empt the
				// possibility of sending that chat channel goroutine any more
				// messages. HOWEVER we can't actually exit until we get the call
				// back from the chat channel telling us we're off the channel.
				//
				doppelgangerState.chatChannelID = 0
				doppelgangerState.chatChannelName = "(no channel)"
				doppelgangerState.cantExitBeforeExitMessageFromChannel = true
				return true, nil
			}
		}
	case "/emote":
		if doppelgangerState.chatChannelID != 0 {
			//
			// If we're not on a channel, we leave what the user typed visible.
			// Otherwise, we backspace out so when it bounces back on the
			// channel, it will replace the line the user typed.
			//
			// We backspace out just what the user typed because the prompt will
			// get backspaced out when the speech comes back.
			err := backspaceOut(doppelgangerState, doppelgangerState.cursorColumn)
			if err != nil {
				return false, err
			}
		}
		if doppelgangerState.userID == 0 {
			//
			// This should be impossible to happen because we don't let the user
			// type any commands unless they have successfully completed the
			// login. Nonetheless if they do somehow get here, we do the
			// sensible thing.
			//
			_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\nYou have to log in before you can talk on a channel.\r\n"))
			return false, err // err can be nil
		} else {
			if doppelgangerState.chatChannelID == 0 {
				//
				// Carriage return because we're not going to backspace out,
				// we're going to go to the next line and give an error message.
				//
				_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\nYou have to join a channel before you can say anything on a channel. Right now you're just talking to yourself.\r\n"))
				return true, err // err can be nil
			} else {
				var newMsg messageFromDoppelgangerToChatChannel
				newMsg.operation = fromDoppelgangerToChatChannelOpTextMessage
				newMsg.userID = doppelgangerState.userID
				newMsg.parameter = emoteParameter
				if doppelgangerState.chatChannelCallback == nil {
					//
					// Should never happen.
					//
					logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: doppelgangerState.chatChannelCallback == nil")
					return false, nil // Try and keep server up
				}
				doppelgangerState.chatChannelCallback <- newMsg
			}
		}
	case "/help":
		_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\n\r\n/list                 -- list channels\r\n/create <channelname> -- create a channel\r\n/join <channelname>   -- join a channel\r\n/who                  -- show who is on the current channel\r\n/exit                 -- exit the current channel\r\n\r\nOnce on a channel:\r\n/say   -- say something on the current channel\r\n/emote -- emote on current channel\r\n/think -- think something on current channel\r\n/sing  -- sing something on current channel\r\n\r\n/help  -- this command\r\n\r\nAbbreviations:\r\n' -- say\r\n; -- emote\r\n\r\n^D log off\r\n\r\n"))
		return true, err // err can be nil
	default:
		//
		// Carriage return needed because user's "return" wasn't echoed.
		//
		_, err := oi.LongWrite(doppelgangerState.writer, []byte("\r\n"))
		if err != nil {
			return false, err
		}
		_, err = oi.LongWrite(doppelgangerState.writer, []byte("\r\nCommand \""+command+"\" not recognized.\r\n"))
		return true, err // err can be nil
	}
	return false, nil
}

//
// We had to break this out into a separate function because this functionality
// has to be shared between the text message and leave chat channel op codes.
// Return value indicates whether telnet goroutine is gone which means our
// entire goroutine needs to be exited (true means exit).
//
func genericTextOutput(doppelgangerState *userInfo, theMessage messageFromChatChannelToDoppelganger) bool {
	var err error
	if theMessage.originator == doppelgangerState.userID {
		// Our own message -- don't backspace out.
		err = backspaceOut(doppelgangerState, doppelgangerState.promptLen+doppelgangerState.cursorColumn)
		_, err = oi.LongWrite(doppelgangerState.writer, []byte(theMessage.parameter+"\r\n"))
	} else {
		//
		// Backspace out before outputting message
		//
		err = backspaceOut(doppelgangerState, doppelgangerState.promptLen+doppelgangerState.cursorColumn)
		if err != nil {
			//
			// We are assuming if we got an error, the network connection is
			// closed, and we need to exit the doppelganger because we are
			// done, too.
			//
			return true
		}
		_, err = oi.LongWrite(doppelgangerState.writer, []byte(theMessage.parameter+"\r\n"))
	}
	if err != nil {
		//
		// We are assuming if we got an error, the network connection is closed,
		// and we need to exit the doppelganger because we are done, too.
		//
		return true
	}
	return false
}

//
// Made this into a separate function because we can detect the user has gone
// away at lots of points (for example in the command loop or in the processing
// of messages from other parts of the system), and want to follow the same
// procedure for exiting a channel (if there is one) no matter where are when
// we notice the user is gone. Users of course can dump their connection any
// time.
//
// Return value indicates whether it is safe to fully exit the doppelganger
// goroutine (true), or whether we can't because we have to get off a chat
// channel first (false).
//
func handleChannelExitProcedure(doppelgangerState *userInfo) bool {
	//
	// If we already sent a chat channel exit command, let's not do it
	// again -- let's be idempotent.
	//
	if doppelgangerState.cantExitBeforeExitMessageFromChannel {
		doppelgangerState.cantExitCount++
		time.Sleep(1 * time.Second)
		if doppelgangerState.cantExitCount >= 1000 {
			//
			// We're going to assume the chat channel goroutine that was
			// supposed to send us a message back telling us we've exited has
			// for some reason thought we weren't in the chat channel to
			// begin with and will never send us the message, so it's safe
			// for us to just go ahead and shut down.
			//
			logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: cantExitBeforeExitMessageFromChannel has looped 1000 times, something is wrong.")
			return true
		}
		return false
	} else {
		//
		// Are we on a chat channel? If so we have to drop it. We send a
		// (partial) /exit command to leave the channel. And we return
		// "false"
		//
		if (doppelgangerState.userID != 0) && (doppelgangerState.chatChannelID != 0) {
			//
			// We're using the -1 as a special flag to prevent exit in between
			// join request and actual join.
			//
			if doppelgangerState.chatChannelID == -1 {
				log.Println("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + ", chatChannelID == -1, skipping sending of exit message.")
			} else {
				var theMessage messageFromDoppelgangerToChannelMaster
				theMessage.operation = fromDoppelgangerToChannelMasterOpExit
				theMessage.userID = doppelgangerState.userID
				theMessage.userName = doppelgangerState.userName
				theMessage.doppelgangerID = doppelgangerState.doppelgangerID
				theMessage.chatChannelID = doppelgangerState.chatChannelID
				theMessage.parameter = ""
				theMessage.doppelgangerCallbackFromChannelMaster = doppelgangerState.incomingFromChannelMaster
				theMessage.doppelgangerCallbackFromChatChannel = doppelgangerState.incomingFromChatChannel
				if global.chanMasterFromDoppelgangerGoChan == nil {
					//
					// Should never happen.
					//
					logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: global.chanMasterFromDoppelgangerGoChan == nil")
					return false // Try and keep server up (kind of laughable if the go channel to the channel master is gone, though)
				}
				global.chanMasterFromDoppelgangerGoChan <- theMessage
			}
			doppelgangerState.chatChannelID = 0
			doppelgangerState.chatChannelName = "(no channel)"
			doppelgangerState.cantExitBeforeExitMessageFromChannel = true
			return false
		} else {
			return true
		}
	}
}

func doppelgangerGoroutine(writer telnet.Writer, userGoChannel <-chan byte) {
	var doppelgangerState userInfo
	doppelgangerState.writer = writer
	doppelgangerState.telnetGoroutineHasGoneAway = false
	doppelgangerState.cantExitBeforeExitMessageFromChannel = false
	doppelgangerState.userID = 0
	doppelgangerState.userName = "(not logged in)"
	doppelgangerState.chatChannelID = 0
	doppelgangerState.chatChannelName = "(no channel)"
	//
	// Buffer size of 1 because there's always only 1 channel master.
	//
	doppelgangerState.incomingFromChannelMaster = make(chan messageFromChannelMasterToDoppelganger, 1)
	//
	// Buffer size of 1 because a given user is only "in" one chat channel
	// at a time.
	//
	doppelgangerState.incomingFromChatChannel = make(chan messageFromChatChannelToDoppelganger, 1)
	//
	// Had to move mode into doppelgangerState so commands (handled by a
	// function to make the code structure simpler) can set the "suppress
	// prompt" mode.
	//
	doppelgangerState.mode = loginUsernameMode
	//
	// info about user attempting to log in -- kept separate to ensure we
	// don't accidentally confuse a user attempting to log in with a user
	// that's actually logged in!
	//
	doppelgangerState.attemptingUserName = ""
	doppelgangerState.attemptingUserNewPassword = ""
	//
	// We use the random number generator to give ourselves an ID. We could
	// use unsigned 64-bit numbers, but I find it's better to avoid unsigned
	// ints unless you really need to. There are edge cases where unsigned
	// ints behave differently from signed ints and don't do what you expect.
	// We do this here, in every doppelganger goroutine, instead of globally
	// because I wasn't sure if the math/rand (non-cryptographic pseudorandom
	// number generater) is thread-safe, i.e. if we did it globally and the
	// system was getting hammered by a million users creating doppelgangers
	// all at once, would it be possible for two of them to call Int63() and
	// get the same number back because the internal seed had not been
	// incremented yet?
	//
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	doppelgangerState.doppelgangerID = rnd.Int63()
	//
	// Switch to full duplex!!
	//
	var err error
	_, err = oi.LongWrite(writer, []byte{255, 251, 3, 255, 251, 1, 13, 10}) // turn on full duplex, turn off local echo
	if err != nil {
		//
		// We are assuming if we got an error, the network connection is
		// closed, and we need to exit the doppelganger because we are
		// done, too. We're before the main loop, so it's impossible for
		// the user to have joined a chat channel, so we're going to go
		// ahead and bail here. Once we're in the loop, though, we'll
		// need to check and see if we're on a chat channel.
		//
		close(doppelgangerState.incomingFromChannelMaster)
		close(doppelgangerState.incomingFromChatChannel)
		logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: turn on full duplex failed.")
		return
	}
	_, err = oi.LongWrite(writer, []byte("Welcome to the Wayne Brain Telnet daemon. Type ^D to exit.\r\n\r\n"))
	if err != nil {
		//
		// We are assuming if we got an error, the network connection is
		// closed, and we need to exit the doppelganger because we are
		// done, too. We're before the main loop, so it's impossible for
		// the user to have joined a chat channel, so we're going to go
		// ahead and bail here. Once we're in the loop, though, we'll
		// need to check and see if we're on a chat channel.
		//
		close(doppelgangerState.incomingFromChannelMaster)
		close(doppelgangerState.incomingFromChatChannel)
		logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: sending welcome message failed.")
		return
	}
	//
	// Finish setup.
	//
	doppelgangerState.promptNeeded = true
	doppelgangerState.controlSequence = make([]byte, 3)
	doppelgangerState.ctrlSeqPosition = 0
	doppelgangerState.lineBuffer = make([]byte, 0)
	doppelgangerState.cursorColumn = 0
	doppelgangerState.prevUsrByte = 0
	doppelgangerState.echoOn = true
	doppelgangerState.backspaceBuffer = make([]byte, 0)
	echoSlice := make([]byte, 1) // just to keep from having to allocate a 1-byte slice over and over
	//
	// Main loop -- prompt the user and process bytes that the user types
	//
	for {
		if doppelgangerState.promptNeeded {
			//
			// ALRIGHTY! This is the main loop where we accept and process
			// user commands. We have to have special modes, though, for
			// username/password and registration. The login process as
			// well is handled by a series of special modes whereby we
			// interpret the commands as usernames and passwords (and
			// prompt user accordingly). Prompt to user depends on what
			// mode we're in.
			//
			doppelgangerState.promptLen = 0
			switch doppelgangerState.mode {
			case loginUsernameMode:
				_, err = oi.LongWrite(writer, []byte("Username: "))
			case loginNewUserYNMode:
				_, err = oi.LongWrite(writer, []byte("Create new account? (y/n) "))
			case loginNewPassword1Mode:
				_, err = oi.LongWrite(writer, []byte("Password for new account: "))
				if err != nil {
					//
					// We are assuming if we got an error, the network
					// connection is closed, and we need to exit doppelganger
					// goroutine because we are done, too.
					//
					doppelgangerState.telnetGoroutineHasGoneAway = true
				}
				doppelgangerState.echoOn = false
			case loginNewPassword2Mode:
				_, err = oi.LongWrite(writer, []byte("Repeat password: "))
				if err != nil {
					//
					// We are assuming if we got an error, the network
					// connection is closed, and we need to exit doppelganger
					// goroutine because we are done, too.
					//
					doppelgangerState.telnetGoroutineHasGoneAway = true
				}
				doppelgangerState.echoOn = false
			case loginRegularPasswordMode:
				_, err = oi.LongWrite(writer, []byte("Password: "))
				if err != nil {
					//
					// We are assuming if we got an error, the network connection is closed, and we need to exit doppelganger goroutine because we are done, too.
					//
					doppelgangerState.telnetGoroutineHasGoneAway = true
				}
				doppelgangerState.echoOn = false
			case loginCommandMode:
				promptBuffer := []byte(doppelgangerState.userName + " #" + doppelgangerState.chatChannelName + "> ")
				//
				// We have to keep track of how many bytes we output so
				// we can backspace out our prompt.  // We have to be
				// able to do this so conversation of other users other
				// than the user appear without the user's prompts on
				// every other line. If the user has a partially typed
				// line, we repeat it so the user doesn't get confused.
				//
				doppelgangerState.promptLen = len(promptBuffer)
				if doppelgangerState.cursorColumn > 0 {
					promptBuffer = append(promptBuffer, doppelgangerState.lineBuffer[:doppelgangerState.cursorColumn]...)
				}
				_, err = oi.LongWrite(writer, promptBuffer)
			default:
				//
				// Should never happen.
				//
				logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: mode for prompt is missing or invalid. mode: " + intToStr(doppelgangerState.mode))
			}
			if err != nil {
				//
				// We are assuming if we got an error, the network
				// connection is closed, and we need to exit the
				// doppelganger because we are done, too.
				//
				doppelgangerState.telnetGoroutineHasGoneAway = true
			}
		}
		//
		// This is the main select where we receive messages from the Telnet
		// goroutine for the user that directly accepts user input.
		//
		select {
		case usrByte, ok := <-userGoChannel:
			//
			// usrByte is the character the user just typed -- note that we
			// allow ourselves to change this value and "pretend" that the
			// user typed something different! (Useful for handling special
			// characters like backspaces, arrow keys, etc).
			//
			if !ok {
				//
				// Whoa, channel closed! Assume user closed connection and
				// exit doppelganger goroutine because the user's Telnet
				// goroutine is gone, too.
				//
				doppelgangerState.telnetGoroutineHasGoneAway = true
				//
				// We actually will hit this point every time through this
				// select loop until we get the message back from the chat
				// channel goroutine that the user is off the channel. Not
				// sure why Go selects work this way but they do.
				//
				usrByte = 0 // special value to signal no further processing
			}
			if (usrByte == 3) || (usrByte == 4) { // user typed ^C or ^D
				//
				// We do this because the telnet handler has been programmed
				// to automatically exit (which closes the connection to
				// the user) when ^C or ^D is typed. So at this point it's
				// safe to assume that goroutine is gone.
				//
				doppelgangerState.telnetGoroutineHasGoneAway = true
				usrByte = 0 // special code to suspend further processing of this character
			}
			if usrByte == 127 {
				//
				// For some reason, on my system, the backspace key sends
				// 127 instead of 8. We'll just pretend they sent an 8.
				//
				usrByte = 8
			}
			doppelgangerState.promptNeeded = false
			if doppelgangerState.ctrlSeqPosition != 0 {
				doppelgangerState.controlSequence[doppelgangerState.ctrlSeqPosition] = usrByte
				doppelgangerState.ctrlSeqPosition++
				usrByte = 0
				//
				// Check for control codes!
				//
				if doppelgangerState.ctrlSeqPosition == 3 {
					if (doppelgangerState.controlSequence[0] == 27) && (doppelgangerState.controlSequence[1] == 91) && (doppelgangerState.controlSequence[2] == 68) {
						//
						// Back arrow -- convert to backspace!
						//
						usrByte = 8
						doppelgangerState.ctrlSeqPosition = 0
					} else {
						if (doppelgangerState.controlSequence[0] == 27) && (doppelgangerState.controlSequence[1] == 91) {
							//
							// Ignore all other arrow keys!
							//
							usrByte = 0
							doppelgangerState.ctrlSeqPosition = 0
						} else {
							//
							// For now, we just ignore all other control sequences.
							//
							usrByte = 0
							doppelgangerState.ctrlSeqPosition = 0
						}
					}
				}
			}
			//
			// Check for control codes.
			//
			if usrByte < 32 {
				if usrByte == 27 {
					//
					// Arrow keys.
					//
					doppelgangerState.controlSequence[0] = usrByte
					doppelgangerState.ctrlSeqPosition = 1
					usrByte = 0
				} else {
					if (usrByte == 10) || (usrByte == 13) {
						if (doppelgangerState.prevUsrByte == 10) || (doppelgangerState.prevUsrByte == 13) {
							//
							// We accept either CR (13) or LF (10) as a "return"
							// character, but if the user sends two in a row of
							// either, we discard the 2nd as superfluous.
							// Throwback to the era of typewriters that amazingly
							// enough we still have to deal with in 2019.
							//
							usrByte = 0
						}
					} else {
						if usrByte != 8 {
							//
							// Keep backspaces, throw away all other control codes.
							//
							usrByte = 0
						}
					}
				}
			}
			//
			// Echo!
			//
			// With special handling for backspaces.
			//
			if doppelgangerState.echoOn {
				//
				// We assign 0 to ourselves as a special value that means
				// transmit nothing.
				//
				if usrByte != 0 {
					if usrByte == 8 {
						// backspace
						if doppelgangerState.cursorColumn >= 1 {
							//
							// We don't allow user to backspace beyond first
							// character (and overwrite our prompt).
							//
							// With backspaces, we have to output a space to
							// blank out the last character.
							//
							_, err = oi.LongWrite(writer, []byte("\b \b"))
							if err != nil {
								//
								// We are assuming if we got an error, the network
								// connection is closed, and we need to exit the
								// doppelganger because we are done, too.
								//
								doppelgangerState.telnetGoroutineHasGoneAway = true
							}
						}
					} else {
						if (usrByte == 10) || (usrByte == 13) {
							//
							// Brrrp! Don't echo the "return" key!!!
							//
						} else {
							//
							// User hit anything else -- echo it back to them.
							//
							echoSlice[0] = usrByte
							_, err = oi.LongWrite(writer, echoSlice)
							if err != nil {
								//
								// We are assuming if we got an error, the network
								// connection is closed, and we need to exit the
								// doppelganger because we are done, too.
								//
								doppelgangerState.telnetGoroutineHasGoneAway = true
							}
						}
					}
				}
			}
			//
			// Manage line buffer.
			//
			if usrByte != 0 {
				if usrByte == 8 {
					//
					// Backspace.
					//
					doppelgangerState.cursorColumn--
					if doppelgangerState.cursorColumn < 0 {
						doppelgangerState.cursorColumn = 0
					}
				} else {
					if usrByte >= 32 {
						//
						// We don't put control codes in the line buffer.
						//
						if doppelgangerState.cursorColumn < len(doppelgangerState.lineBuffer) {
							doppelgangerState.lineBuffer[doppelgangerState.cursorColumn] = usrByte
							doppelgangerState.cursorColumn++
						} else {
							doppelgangerState.lineBuffer = append(doppelgangerState.lineBuffer, usrByte)
							doppelgangerState.cursorColumn = len(doppelgangerState.lineBuffer)
						}
					}
				}
			}
			//
			// DO IT
			// Here's where we actually execute commands!
			//
			if (usrByte == 10) || (usrByte == 13) {
				//
				// Note that even though we put bytes in the line buffer and
				// convert to a string here, which theoretically converts UTF-8
				// multi-byte characters into actual characters (runes in Go),
				// because of the nature of the Telnet protocol itself, the user
				// can't actually send multi-byte characters and have it work.
				// Brrrrrp! I tested this and UTF-8 characters actually work!
				// Tested with Chinese characters. Holy Chinese characters,
				// Batman! The people who created UTF-8 really did a good job,
				// it's backward compatible with the telnet protocol, a protocol
				// invented way before UTF-8 existed.
				//
				command := trim(string(doppelgangerState.lineBuffer[:doppelgangerState.cursorColumn]))
				//
				//
				if command == "" { // ignore blank lines
					doppelgangerState.promptNeeded = false
				} else {
					//
					// How we interpret the command depends on what
					// mode we're in.
					//
					switch doppelgangerState.mode {
					case loginUsernameMode:
						doppelgangerState.attemptingUserName = command
						exists, err := userExists(doppelgangerState.attemptingUserName)
						if err != nil {
							//
							// Eh, we really don't know what to do here.
							// Log error for human to review (hopefully).
							//
							log.Println(err)
							//
							// What now? We can't exit because the user is
							// still connected.
							//
							_, err = oi.LongWrite(writer, []byte("A database error occurred.\r\n"))
							if err != nil {
								//
								// We are assuming if we got an error, the network
								// connection is closed, and we need to exit the
								// doppelganger because we are done, too.
								//
								doppelgangerState.telnetGoroutineHasGoneAway = true
							}
						}
						if !exists {
							//
							// Prepend carriage return because user's carriage
							// return is not echoed.
							//
							_, err = oi.LongWrite(writer, []byte("\r\nThat username does not exist on this system. "))
							if err != nil {
								//
								// We are assuming if we got an error, the network
								// connection is closed, and we need to exit the
								// doppelganger because we are done, too.
								//
								doppelgangerState.telnetGoroutineHasGoneAway = true
							}
							doppelgangerState.mode = loginNewUserYNMode
						} else {
							//
							// Carriage return because user's carriage return
							// is not echoed.
							//
							_, err = oi.LongWrite(writer, []byte("\r\n"))
							if err != nil {
								//
								// We are assuming if we got an error, the network
								// connection is closed, and we need to exit the
								// doppelganger because we are done, too.
								//
								doppelgangerState.telnetGoroutineHasGoneAway = true
							}
							doppelgangerState.mode = loginRegularPasswordMode
						}
						doppelgangerState.promptNeeded = true
					case loginNewUserYNMode:
						//
						// Prepend carriage return because user's carriage
						// return is not echoed.
						//
						_, err = oi.LongWrite(writer, []byte("\r\n"))
						if err != nil {
							//
							// We are assuming if we got an error, the network
							// connection is closed, and we need to exit the
							// doppelganger because we are done, too.
							//
							doppelgangerState.telnetGoroutineHasGoneAway = true
						}
						//
						// Default to going back to username instead of
						// advancing.
						//
						doppelgangerState.mode = loginUsernameMode
						if len(command) >= 1 {
							if (command[0] == 'Y') || (command[0] == 'y') {
								doppelgangerState.mode = loginNewPassword1Mode
							}
						}
						doppelgangerState.promptNeeded = true
					case loginNewPassword1Mode:
						//
						// Line feed because the user's return for the
						// password wasn't echoed.
						//
						_, err = oi.LongWrite(writer, []byte{13, 10})
						if err != nil {
							//
							// We are assuming if we got an error, the network
							// connection is closed, and we need to exit the
							// doppelganger because we are done, too.
							//
							doppelgangerState.telnetGoroutineHasGoneAway = true
						}
						if len(command) < 4 {
							_, err = oi.LongWrite(writer, []byte("Please enter a password at least 4 characters long.\r\n"))
							if err != nil {
								//
								// We are assuming if we got an error, the network
								// connection is closed, and we need to exit the
								// doppelganger because we are done, too.
								//
								doppelgangerState.telnetGoroutineHasGoneAway = true
							}
						} else {
							doppelgangerState.attemptingUserNewPassword = command
							doppelgangerState.mode = loginNewPassword2Mode
						}
						doppelgangerState.promptNeeded = true
					case loginNewPassword2Mode:
						//
						// Line feed because the user's return for the
						// password wasn't echoed.
						//
						_, err = oi.LongWrite(writer, []byte{13, 10})
						//
						// Default to trying again.
						//
						doppelgangerState.mode = loginNewPassword1Mode
						if command != doppelgangerState.attemptingUserNewPassword {
							_, err = oi.LongWrite(writer, []byte("Confirmation password did not match. Please try again.\r\n"))
							if err != nil {
								//
								// We are assuming if we got an error, the network
								// connection is closed, and we need to exit the
								// doppelganger because we are done, too.
								//
								doppelgangerState.telnetGoroutineHasGoneAway = true
							}
						} else {
							err = createUser(doppelgangerState.attemptingUserName, doppelgangerState.attemptingUserNewPassword)
							if err != nil {
								_, err = oi.LongWrite(writer, []byte("An error occurred while creating your account: "+err.Error()+"\r\n"))
							} else {
								_, err = oi.LongWrite(writer, []byte("Your new account has been created. Please log in as you will normally.\r\n"))
								if err != nil {
									//
									// We are assuming if we got an error, the network
									// connection is closed, and we need to exit the
									// doppelganger because we are done, too.
									//
									doppelgangerState.telnetGoroutineHasGoneAway = true
								}
							}
							doppelgangerState.mode = loginUsernameMode
						}
						doppelgangerState.promptNeeded = true
						doppelgangerState.echoOn = true
					case loginRegularPasswordMode:
						password := command
						doppelgangerState.userID, doppelgangerState.userName, err = login(doppelgangerState.attemptingUserName, password)
						if doppelgangerState.userID == 0 {
							//
							// Leading carriage return needed because user's
							// "return" wasn't echoed.
							//
							_, err = oi.LongWrite(writer, []byte("\r\nIncorrect password.\r\n"))
							if err != nil {
								//
								// We are assuming if we got an error, the network
								// connection is closed, and we need to exit the
								// doppelganger because we are done, too.
								//
								doppelgangerState.telnetGoroutineHasGoneAway = true
							}
							doppelgangerState.mode = loginUsernameMode
						} else {
							//
							// Carriage return needed because user's "return"
							// wasn't echoed.
							//
							_, err = oi.LongWrite(writer, []byte("\r\nYou are logged in. Use /help for help with commands.\r\n"))
							if err != nil {
								//
								// We are assuming if we got an error, the network
								// connection is closed, and we need to exit the
								// doppelganger because we are done, too.
								//
								doppelgangerState.telnetGoroutineHasGoneAway = true
							}
							doppelgangerState.mode = loginCommandMode
						}
						doppelgangerState.promptNeeded = true
						doppelgangerState.echoOn = true
					case loginCommandMode:
						//
						// Ignore blank lines.
						//
						command = trim(command)
						if len(command) > 0 {
							//
							//
							// if ', substitute "/say"
							// if ;, substitute "/emote"
							//
							if command[0] == '\'' {
								command = "/say " + trim(command[1:])
							}
							if command[0] == ';' {
								command = "/emote " + trim(command[1:])
							}
							//
							// At this point, all commands should start with "/".
							//
							if command[0] != '/' {
								command = "/say " + trim(command)
							}
							//
							// At this point, we should have a command. Anything
							// not a command will have been turned into a "say".
							//
							// In order to make this code more tidy, we hand
							// off execution to a special function, doCommand,
							// to actually execute the commands. Besides, who
							// knows, maybe it will be handy for commands to
							// be scriptable or something some day, and having
							// this entry point will be useful. We hand off a
							// pointer to our state (for logged in users) so
							// the doCommand function can modify all the
							// relevant state as if the code were all right
							// here.
							//
							doppelgangerState.promptNeeded, err = doCommand(&doppelgangerState, command)
							if err != nil {
								//
								// We are assuming if we got an error, the network
								// connection is closed, and we need to exit the
								// doppelganger because we are done, too.
								//
								doppelgangerState.telnetGoroutineHasGoneAway = true
							}
						}
					default:
						//
						// Should never happen.
						//
						logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: login mode for command processing is missing or invalid: " + intToStr(doppelgangerState.mode))
					}
					//
					// Reset line buffer for next line.
					//
					doppelgangerState.cursorColumn = 0
				}
			}
		case response, ok := <-doppelgangerState.incomingFromChannelMaster:
			if !ok {
				//
				// Whoa, channel closed! We lost our own response
				// channel?? This should never happen. Log and
				// bail.
				//
				logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: incomingFromChannelMaster go channel has unexpectedly closed.")
				close(doppelgangerState.incomingFromChannelMaster)
				close(doppelgangerState.incomingFromChatChannel)
				return
			}
			//
			// We have to preceed messages to the user with a CR+LF
			// because we've probably already outputted a prompt before
			// getting here.
			//
			switch response.operation {
			case fromChannelMasterToDoppelgangerOpGenericText:
				//
				// For channel messages, we leave what the user
				// has typed so far?
				//
				if !doppelgangerState.telnetGoroutineHasGoneAway {
					_, err = oi.LongWrite(writer, []byte("\r\n"+response.msgToUser+"\r\n"))
					if err != nil {
						//
						// We are assuming if we got an error, the network
						// connection is closed, and we need to exit the
						// doppelganger because we are done, too.
						//
						doppelgangerState.telnetGoroutineHasGoneAway = true
					}
				}
			case fromChannelMasterToDoppelgangerOpJoinDenied:
				//
				// This is the same as op generic text, except in addition, we
				// clear out the channel id, because we set the channel id to
				// the special hack -1 value when we've asked to join a channel
				// but not actually joined. If we've lost the Telnet connection,
				// we need to clear the flag that would otherwise keep this
				// goroutine from exiting.
				//
				doppelgangerState.chatChannelID = 0
				if !doppelgangerState.telnetGoroutineHasGoneAway {
					_, err = oi.LongWrite(writer, []byte("\r\n"+response.msgToUser+"\r\n"))
					if err != nil {
						//
						// We are assuming if we got an error, the network
						// connection is closed, and we need to exit the
						// doppelganger because we are done, too.
						//
						// Our user (socket connection) is gone, but we
						// are not on a channel, can exit no problem.
						//
						doppelgangerState.telnetGoroutineHasGoneAway = true
					}
				}
				if doppelgangerState.telnetGoroutineHasGoneAway {
					//
					// Our join failed, we will exit immediately.
					//
					doppelgangerState.cantExitBeforeExitMessageFromChannel = false
				}
			default:
				//
				// Should never happen.
				//
				logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: unexpected opcode from channel master: " + intToStr(response.operation))
			}
			doppelgangerState.promptNeeded = true
		case theMessage, ok := <-doppelgangerState.incomingFromChatChannel:
			if !ok {
				//
				// Whoa, channel closed! We lost our own response channel??
				// This should never happen. Log and bail.
				//
				logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: callback channel for chat channels unexpectedly closed.")
				close(doppelgangerState.incomingFromChannelMaster)
				close(doppelgangerState.incomingFromChatChannel)
				return
			}
			switch theMessage.operation {
			case fromChatChannelToDoppelgangerOpJoinDenied:
				if !doppelgangerState.telnetGoroutineHasGoneAway {
					_, err = oi.LongWrite(writer, []byte("\r\nRequest to join channel denied: "+theMessage.parameter+"\r\n"))
					if err != nil {
						//
						// We are assuming if we got an error, the network connection
						// is closed, and we need to exit the doppelganger because we
						// are done, too.
						//
						doppelgangerState.telnetGoroutineHasGoneAway = true
					}
				}
			case fromChatChannelToDoppelgangerOpJoined:
				doppelgangerState.chatChannelID = theMessage.chatChannelID
				doppelgangerState.chatChannelName = theMessage.parameter
				doppelgangerState.chatChannelCallback = theMessage.chatChannelCallback
				if doppelgangerState.chatChannelCallback == nil {
					//
					// Should never happen.
					//
					logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: doppelgangerState.chatChannelCallback == nil")
				}
				if !doppelgangerState.telnetGoroutineHasGoneAway {
					_, err = oi.LongWrite(writer, []byte("\r\nYou have joined #"+doppelgangerState.chatChannelName+"\r\n"))
					if err != nil {
						//
						// We are assuming if we got an error, the network connection is
						// closed, and we need to exit the doppelganger because we are
						// done, too.
						//
						doppelgangerState.telnetGoroutineHasGoneAway = true
					}
				}
				if doppelgangerState.telnetGoroutineHasGoneAway {
					//
					// If we lost the user while the join request was taking place,
					// we need to do a normal exit procedure, which means we have
					// to reset the cantExitBeforeExitMessageFromChannel flag here
					// so the procedure will start from the beginning.
					//
					doppelgangerState.cantExitBeforeExitMessageFromChannel = false
				}
			case fromChatChannelToDoppelgangerOpTextMessage:
				shutdown := genericTextOutput(&doppelgangerState, theMessage)
				if shutdown {
					//
					// It's possible that the first time we realize a user has gone
					// away is when *another* user types a message and we try to
					// relay that message to the first user.
					//
					doppelgangerState.telnetGoroutineHasGoneAway = true
				}
			case fromChatChannelToDoppelgangerOpTextExit:
				//
				// Here is the point where, if the user went away, the message
				// we send through the channel master and the chat channel to
				// tell them that the user has gone away comes back to us. We
				// need some logic to decide what to do. If the
				// telnetGoroutineHasGoneAway flag is set, there is no reason
				// for us not to exit and shut down this goroutine, so we do
				// that. If telnetGoroutineHasGoneAway is not set but
				// cantExitBeforeExitMessageFromChannel is set, that means the
				// user typed an exit command, and since we have to be able to
				// receive messages from other users on the channel while that
				// is being processed, we stay running, and if the user hasn't
				// quit by the time the exit message gets back here, we just
				// clear the cantExitBeforeExitMessageFromChannel flag and keep
				// going, and allow the user to join a new channel.
				//
				if doppelgangerState.telnetGoroutineHasGoneAway {
					if doppelgangerState.doppelgangerID == theMessage.leavingDoppelgangerID {
						close(doppelgangerState.incomingFromChannelMaster)
						close(doppelgangerState.incomingFromChatChannel)
						return
					}
				}
				if doppelgangerState.doppelgangerID == theMessage.leavingDoppelgangerID {
					//
					// If we're seeing our own exit bounced back, we can always
					// exit, so we can confidently set
					// cantExitBeforeExitMessageFromChannel to false.
					//
					doppelgangerState.cantExitBeforeExitMessageFromChannel = false
				}
				shutdown := genericTextOutput(&doppelgangerState, theMessage)
				if shutdown {
					doppelgangerState.telnetGoroutineHasGoneAway = true
				}
				if doppelgangerState.doppelgangerID == theMessage.leavingDoppelgangerID {
					//
					// This clearing of the chat channel ID should be reduntant as
					// it should have been done the instant the user typed the exit
					// command, but we do it again here just to make sure. We do
					// this last, AFTER doing all the shutdown-related stuff, in
					// case the channel ID is needed.
					//
					doppelgangerState.chatChannelID = 0
					doppelgangerState.chatChannelName = "(no channel)"
				}
			default:
				//
				// Should never happen.
				//
				logError("doppelganger ID " + int64ToStr(doppelgangerState.doppelgangerID) + " user ID " + int64ToStr(doppelgangerState.userID) + " error: unexpected opcode received from chat channel: " + intToStr(theMessage.operation))
			}
			doppelgangerState.promptNeeded = true
		}
		if doppelgangerState.telnetGoroutineHasGoneAway {
			shutdown := handleChannelExitProcedure(&doppelgangerState)
			if shutdown {
				close(doppelgangerState.incomingFromChannelMaster)
				close(doppelgangerState.incomingFromChatChannel)
				return
			}
		}
	}
}
