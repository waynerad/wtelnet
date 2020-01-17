package main

import (
	"log"
	"os"
	"time"
)

//
// Chat Channel state. memberList maps userIDs to the Go channel used to
// communicate with the chat channel goroutine. BRRRP!! No, it maps
// doppelganger IDs (but keeps track of user IDs), so that the same user
// can log in more than once and participate in multiple conversations or
// even talk to themselves on the same channel.
//

type userEntry struct {
	userID               int64
	userName             string
	doppelgangerCallback chan messageFromChatChannelToDoppelganger
}

type chatChannelInfo struct {
	chatChannelID            int64
	chatChannelName          string
	memberList               map[int64]userEntry
	convoLogFile             *os.File
	incomingFromDoppelganger chan messageFromDoppelgangerToChatChannel
}

//
// Chat Channel functions
//

//
// Return value indicates "true" if a shutdown messages was received and the
// whole goroutine needs to shut down.
//
func processMessageFromChannelMaster(chatChannelState *chatChannelInfo, theMessage messageFromChannelMasterToChatChannel) bool {
	switch theMessage.operation {
	case fromChannelMasterToChatChanOpJoin:
		if len(chatChannelState.memberList) >= 6 {
			//
			// Channel is full! No more users allowed.
			//
			// Message back to channel master.
			//
			var deniedMsg messageFromChatChannelToChannelMaster
			deniedMsg.operation = fromChatChannelToChannelMasterOpJoinDenied
			deniedMsg.userID = theMessage.userID
			deniedMsg.doppelgangerID = theMessage.doppelgangerID
			deniedMsg.chatChannelID = chatChannelState.chatChannelID
			if global.chanMasterFromChatChannelGoChan == nil {
				//
				// Should never happen.
				//
				logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " error: global.chanMasterFromChatChannelGoChan == nil")
				return false // Try to keep server up.
			}
			global.chanMasterFromChatChannelGoChan <- deniedMsg
			//
			// Message back to user (doppelganger)
			//
			var newMsg messageFromChatChannelToDoppelganger
			newMsg.operation = fromChatChannelToDoppelgangerOpJoinDenied
			newMsg.originator = 0 // special value that means nobody -- this message is from the channel itself
			newMsg.chatChannelID = chatChannelState.chatChannelID
			newMsg.leavingDoppelgangerID = 0
			newMsg.parameter = "Channel is full"
			if theMessage.doppelgangerCallback == nil {
				//
				// Should never happen.
				//
				logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " error: theMessage.doppelgangerCallback == nil")
				return false // Try to keep server up.
			}
			theMessage.doppelgangerCallback <- newMsg
		} else {
			//
			// Send message letting user know we've let them on the channel.
			//
			var newMsg messageFromChatChannelToDoppelganger
			newMsg.operation = fromChatChannelToDoppelgangerOpJoined
			newMsg.originator = 0 // special value that means nobody -- this message is from the channel itself
			if chatChannelState.chatChannelID == 0 {
				//
				// Should never happen.
				//
				logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " error: chatChannelState.chatChannelID == 0")
				return false // Try to keep server up.
			}
			newMsg.chatChannelID = chatChannelState.chatChannelID
			newMsg.leavingDoppelgangerID = 0
			newMsg.parameter = chatChannelState.chatChannelName
			newMsg.chatChannelCallback = chatChannelState.incomingFromDoppelganger
			if newMsg.chatChannelCallback == nil {
				//
				// Should never happen.
				//
				logError("chatChannel channel" + int64ToStr(chatChannelState.chatChannelID) + " error: newMsg.chatChannelCallback == nil")
				return false // Try to keep server up.
			}
			if theMessage.doppelgangerCallback == nil {
				//
				// Should never happen.
				//
				logError("chatChannel channel" + int64ToStr(chatChannelState.chatChannelID) + " error: theMessage.doppelgangerCallback == nil")
				return false // Try to keep server up.
			}
			theMessage.doppelgangerCallback <- newMsg
			//
			// Add user to the chat channel's member list.
			//
			var entryForList userEntry
			entryForList.userID = theMessage.userID
			entryForList.userName = theMessage.userName
			entryForList.doppelgangerCallback = theMessage.doppelgangerCallback
			chatChannelState.memberList[theMessage.doppelgangerID] = entryForList
			//
			// Tell the user who else is on the channel
			//
			tellWhoIsOnChannel(chatChannelState, theMessage.doppelgangerCallback)
			//
			// Tell everyone else a new user has joined the channel
			//
			for _, memberInfo := range chatChannelState.memberList {
				if memberInfo.userID != theMessage.userID {
					var announceMsg messageFromChatChannelToDoppelganger
					announceMsg.operation = fromChatChannelToDoppelgangerOpTextMessage
					announceMsg.originator = 0 // special value that means nobody -- this message is from the channel itself
					announceMsg.chatChannelID = chatChannelState.chatChannelID
					if chatChannelState.chatChannelID == 0 {
						//
						// Should never happen.
						//
						logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " error: chatChannelState.chatChannelID == 0")
						return false // Try to keep server up.
					}
					announceMsg.parameter = theMessage.userName + " has joined #" + chatChannelState.chatChannelName
					announceMsg.chatChannelCallback = chatChannelState.incomingFromDoppelganger
					if announceMsg.chatChannelCallback == nil {
						//
						// Should never happen.
						//
						logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " announceMsg.chatChannelCallback == nil")
						return false // Try to keep server up.
					}
					memberInfo.doppelgangerCallback <- announceMsg
				}
			}
			logConversationMessage(chatChannelState.convoLogFile, timeNow()+" <"+theMessage.userName+" has JOINED #"+chatChannelState.chatChannelName+">\n")
		}
	case fromChannelMasterToChatChanOpWho:
		tellWhoIsOnChannel(chatChannelState, theMessage.doppelgangerCallback)
	case fromChannelMasterToChatChanOpExit:
		//
		// Tell everyone user has left
		//
		for _, memberInfo := range chatChannelState.memberList {
			var announceMsg messageFromChatChannelToDoppelganger
			announceMsg.operation = fromChatChannelToDoppelgangerOpTextExit
			announceMsg.originator = 0 // special value that means nobody -- this message is from the channel itself
			announceMsg.chatChannelID = chatChannelState.chatChannelID
			announceMsg.leavingDoppelgangerID = theMessage.doppelgangerID
			if chatChannelState.chatChannelID == 0 {
				//
				// Should never happen.
				//
				logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " error: chatChannelState.chatChannelID == 0")
				return false // Try to keep server up.
			}
			if memberInfo.userID == theMessage.userID {
				announceMsg.parameter = "You left #" + chatChannelState.chatChannelName
			} else {
				announceMsg.parameter = theMessage.userName + " has left #" + chatChannelState.chatChannelName
			}
			announceMsg.chatChannelCallback = chatChannelState.incomingFromDoppelganger
			if announceMsg.chatChannelCallback == nil {
				//
				// Should never happen.
				//
				logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " error: announceMsg.chatChannelCallback == nil")
				return false // Try to keep server up.
			}
			if memberInfo.doppelgangerCallback == nil {
				//
				// Should never happen.
				//
				logError("chatChannel channel" + int64ToStr(chatChannelState.chatChannelID) + " error: memberInfo.doppelgangerCallback == nil")
			}
			time.Sleep(10) // 10 nanoseconds -- we just want to give other goroutines a chance to run here
			memberInfo.doppelgangerCallback <- announceMsg
		}
		logConversationMessage(chatChannelState.convoLogFile, timeNow()+" <"+theMessage.userName+" has EXITED #"+chatChannelState.chatChannelName+">\n")
		delete(chatChannelState.memberList, theMessage.doppelgangerID)
	case fromChannelMasterToChatChanOpShutdown:
		//
		// Return true will signal that the whole goroutine should exit and free
		// up all our channels for the garbage collector.
		//
		return true
	default:
		//
		// Should never happen.
		//
		logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " error: unrecognized case for operation from channel master: " + intToStr(theMessage.operation))
	}
	return false
}

func tellWhoIsOnChannel(chatChannelState *chatChannelInfo, doppelgangerCallback chan messageFromChatChannelToDoppelganger) {
	memberStr := ""
	for _, memberInfo := range chatChannelState.memberList {
		memberStr += ", " + memberInfo.userName
	}
	var whoMsg messageFromChatChannelToDoppelganger
	whoMsg.operation = fromChatChannelToDoppelgangerOpTextMessage
	whoMsg.originator = 0 // special value that means nobody -- this message is from the channel itself
	if chatChannelState.chatChannelID == 0 {
		//
		// Should never happen.
		//
		logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " error: chatChannelState.chatChannelID == 0")
		return // Try to keep server up.
	}
	whoMsg.chatChannelID = chatChannelState.chatChannelID
	whoMsg.leavingDoppelgangerID = 0
	whoMsg.parameter = "On this channel: " + memberStr[2:]
	whoMsg.chatChannelCallback = chatChannelState.incomingFromDoppelganger
	if whoMsg.chatChannelCallback == nil {
		//
		// Should never happen.
		//
		logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " error: whoMsg.chatChannelCallback == nil")
		return // Try to keep server up.
	}
	if doppelgangerCallback == nil {
		//
		// Should never happen.
		//
		logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " error: doppelgangerCallback == nil")
		return // Try to keep server up.
	}
	doppelgangerCallback <- whoMsg
}

// We do this close as a separate function, rather than just "defer close", so we can catch and log errors.
func closeConversationLog(convoLogFile *os.File) {
	err := convoLogFile.Close()
	if err != nil {
		//
		// We don't use Fatal because we want to keep the server up and server
		// users as much as possible. But we log the error so we know about it
		// and can fix it.
		//
		log.Println(err)
	}
}

func distributeMessageToEveryoneInChatChannel(chatChannelState *chatChannelInfo, theMessage messageFromDoppelgangerToChatChannel) {
	for _, chatChanInf := range chatChannelState.memberList {
		var newMsg messageFromChatChannelToDoppelganger
		newMsg.operation = fromChatChannelToDoppelgangerOpTextMessage
		newMsg.originator = theMessage.userID
		newMsg.chatChannelID = chatChannelState.chatChannelID
		newMsg.leavingDoppelgangerID = 0
		newMsg.parameter = theMessage.parameter
		chatChanInf.doppelgangerCallback <- newMsg
	}
}

func logConversationMessage(convoLogFile *os.File, message string) {
	msgAsBytes := []byte(message)
	numBytes, err := convoLogFile.Write(msgAsBytes)
	if err != nil {
		//
		// We log the error so we know about it, but otherwise do everything we
		// can to keep the server up and running and giving users the service
		// they expect.
		//
		log.Println(err)
		return
	}
	if numBytes != len(msgAsBytes) {
		//
		// What? We didn't log all the bytes? Let's make sure they all get
		// written out. In practice this should rarely happen, but it is
		// possible, and there are variations by operating system as to whether
		// it processes all the bytes in the Write() call or not.
		//
		numWritten := numBytes
		for numWritten < len(msgAsBytes) {
			numBytes, err = convoLogFile.Write(msgAsBytes[numWritten:])
			if err != nil {
				//
				// As before, we log the error so we know about it, but otherwise
				// do everything we can to keep the server up and running and giving
				// users the service they expect.
				//
				log.Println(err)
				return
			}
			numWritten += numBytes
		}
	}
}

//
// DO IT
// Goroutine for chat channels
//

func chatChannelGoRoutine(chatChannelID int64, chatChannelName string, firstMessage messageFromChannelMasterToChatChannel, incomingFromChannelMaster chan messageFromChannelMasterToChatChannel) {
	//
	// Now we set up our own state and process the first message, which is
	// passed in because receiving it on a channel would just mean another
	// unnecessary trip through the channelmaster's select-loop.
	//
	var chatChannelState chatChannelInfo
	chatChannelState.chatChannelID = chatChannelID
	chatChannelState.chatChannelName = chatChannelName
	//
	// memberList originally mapped userID's to user info, but, that
	// prevented the same user ID from being in the list more than once. I
	// tried putting the same user ID in the memberList twice by changing it
	// from a map to a list, but when a user exited a chat channel, we had
	// no way of telling the two entries apart. To solve this, we assign
	// doppelganger IDs to the doppelgangers, and even if we have two
	// doppelgangers with the same user ID, we can tell them apart. The
	// memberList was switched back to a map, but this time going from
	// doppelganger IDs to user info.
	//
	chatChannelState.memberList = make(map[int64]userEntry)
	//
	// Buffer size per number of users in channel -- you can lower this if
	// you lower the channel limit.
	//
	chatChannelState.incomingFromDoppelganger = make(chan messageFromDoppelgangerToChatChannel, 128)
	//
	// START logging the conversation! If the file doesn't exist, create
	// it. If it does exist, append to the file. NOTE: We are logging in the
	// current directory! We should probably define a log file directory.
	//
	var err error
	chatChannelState.convoLogFile, err = os.OpenFile(deslash(chatChannelName)+".channel.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	//
	// We do this close as a separate function, rather than just "defer
	// close", so we can catch and log errors.
	//
	defer closeConversationLog(chatChannelState.convoLogFile)
	shutdown := processMessageFromChannelMaster(&chatChannelState, firstMessage)
	if shutdown {
		//
		// It would actually be pretty bizarre if the first message was a
		// shutdown message -- it should be impossible to end up in here...
		//
		close(incomingFromChannelMaster)
		close(chatChannelState.incomingFromDoppelganger)
		return
	}
	//
	// Now we go into our own select-loop to process messages from the
	// channel master or from users.
	//
	for {
		select {
		case theMessage, ok := <-incomingFromChannelMaster:
			if !ok {
				//
				// Whoa! The channel is closed. What to do? We don't know.
				// Log and bail.
				//
				log.Println("chatChannelGoRoutine: incomingFromChannelMaster has unexpectedly closed!")
				close(incomingFromChannelMaster)
				close(chatChannelState.incomingFromDoppelganger)
				return
			}
			//
			// We have to pass off to a function here because we have to
			// be able to call the same function on the first message.
			//
			shutdown = processMessageFromChannelMaster(&chatChannelState, theMessage)
			if shutdown {
				if len(chatChannelState.memberList) != 0 {
					log.Println("Chat channel exited without empty member list! Channel ID " + int64ToStr(chatChannelState.chatChannelID))
				}
				close(incomingFromChannelMaster)
				close(chatChannelState.incomingFromDoppelganger)
				return
			}
		case theMessage, ok := <-chatChannelState.incomingFromDoppelganger:
			if !ok {
				//
				// Whoa! The channel is closed. What to do? We don't know. Log and bail.
				//
				log.Println("chatChannelGoRoutine: incomingFromDoppelganger has unexpectedly closed!")
				close(incomingFromChannelMaster)
				close(chatChannelState.incomingFromDoppelganger)
				return
			}
			switch theMessage.operation {
			case fromDoppelgangerToChatChannelOpTextMessage:
				distributeMessageToEveryoneInChatChannel(&chatChannelState, theMessage)
				logConversationMessage(chatChannelState.convoLogFile, timeNow()+" "+theMessage.parameter+"\n")
			default:
				//
				// Should never happen.
				//
				logError("chatChannel channel " + int64ToStr(chatChannelState.chatChannelID) + " Unexpected message operation code from doppelganger: " + intToStr(theMessage.operation))
			}
		}
	}
}
