package main

import (
	"fmt"
	"log"
)

//
// Structure for the channel master to keep track of info for each chat channel
// -- at the moment this consists of just the go channel used to communicate
// with the chat channel and the member count, which when decremented to zero
// will result in a shutdown message being sent to the chat channel and the go
// channel used to communicate with it being released.
//

type perChatChanInfo struct {
	memberCount         int
	chatChannelCallback chan messageFromChannelMasterToChatChannel
}

//
// Channel master functions
//

func getChatchannelID(chatChannelName string) (int64, error) {
	cmd := "SELECT channelid FROM channel WHERE channelname = ?;"
	stmtSel, err := global.db.Prepare(cmd)
	if err != nil {
		return 0, err
	}
	rows, err := stmtSel.Query(chatChannelName)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var channelID int64
	channelID = 0
	for rows.Next() {
		err = rows.Scan(&channelID)
		if err != nil {
			return 0, err
		}
	}
	return channelID, nil // channelID can be 0
}

func joinChatChannel(runningChatchannelMap map[int64]*perChatChanInfo, userID int64, userName string, doppelgangerID int64, chatChannelID int64, chatChannelName string, doppelgangerCallback chan messageFromChatChannelToDoppelganger) {
	if doppelgangerCallback == nil {
		//
		// Should never happen.
		//
		logError("channel master error: doppelgangerCallback == nil")
	}
	_, exists := runningChatchannelMap[chatChannelID]
	if !exists {
		//
		// Chatchannel's goroutine does not exist, so we have to launch it.
		// We'll add an entry to the list now so we don't launch it twice.
		// But we'll set it's go channel to nil to indicate we can't
		// communicate on it yet.
		//
		if chatChannelID == 0 {
			//
			// Should never happen.
			//
			logError("channel master error: joinChatChannel: chatChannelID == 0")
			return // Try and keep server up
		}
		//
		// We send the first message as a parameter to the function we call
		// to launch the chat channel's go routine. We could also send it as
		// a separate message on the channel, in which case it would execute
		// later (hopefully on the first loop through of the chat channel
		// goroutine's select loop) rather than immediately. We're going to
		// go ahead and do it immediately, since we can.
		//
		// First message: user requests permission to join the channel.
		//
		var firstMessage messageFromChannelMasterToChatChannel
		firstMessage.operation = fromChannelMasterToChatChanOpJoin
		firstMessage.userID = userID
		firstMessage.userName = userName
		firstMessage.doppelgangerID = doppelgangerID
		firstMessage.doppelgangerCallback = doppelgangerCallback
		//
		// Buffer size of one because there can't be more than one channel
		// master.
		//
		incomingFromChannelMaster := make(chan messageFromChannelMasterToChatChannel, 1)
		//
		// Add to our list of running channels.
		//
		var newChatChan perChatChanInfo
		newChatChan.memberCount = 1
		newChatChan.chatChannelCallback = incomingFromChannelMaster
		runningChatchannelMap[chatChannelID] = &newChatChan // Use a pointer to get arround "cannot assign to struct field in map" error
		//
		// Launch chatChannel.
		//
		go chatChannelGoRoutine(chatChannelID, chatChannelName, firstMessage, incomingFromChannelMaster)
		return
	}
	//
	// chatchannel does exist -- connect up with running chatchanel. If we
	// haven't gotten called back with the go channel for this chatchannel,
	// we are screwed. But at least we'll know from the nil check. If we
	// can't send the join request, this join request will just get dropped
	// on the floor!! We have to ask the user to attempt it again.
	//
	if runningChatchannelMap[chatChannelID] == nil {
		//
		// This should be impossible...
		//
		logError("channel master error: runningChatchannelMap[chatChannelID] == nil")
	} else {
		runningChatchannelMap[chatChannelID].memberCount++
		var theMessage messageFromChannelMasterToChatChannel
		theMessage.operation = fromChannelMasterToChatChanOpJoin
		theMessage.userID = userID
		theMessage.userName = userName
		theMessage.doppelgangerID = doppelgangerID
		theMessage.doppelgangerCallback = doppelgangerCallback
		if runningChatchannelMap[chatChannelID] == nil {
			//
			// Should never happen.
			//
			logError("channel master error: runningChatchannelMap[chatChannelID] == nil")
			return // Try to keep server up.
		}
		runningChatchannelMap[chatChannelID].chatChannelCallback <- theMessage
	}
}

func whoIsOnChatChannel(runningChatchannelMap map[int64]*perChatChanInfo, userID int64, userName string, doppelgangerID int64, chatChannelID int64, doppelgangerCallback chan messageFromChatChannelToDoppelganger) {
	//
	// Made this a separate function to make the extra error checking
	// easier.
	//
	if doppelgangerCallback == nil {
		//
		// Should never happen.
		//
		logError("channel master error: doppelgangerCallback == nil")
		return // Try to keep server up.
	}
	_, exists := runningChatchannelMap[chatChannelID]
	if !exists {
		//
		// Should never happen.
		//
		logError("channel master error: runningChatchannelMap[chatChannelID] does not exist")
		return // Try to keep server up.
	}
	if runningChatchannelMap[chatChannelID] == nil {
		//
		// Should never happen.
		//
		logError("channel master error: runningChatchannelMap[chatChannelID] == nil")
		return // Try to keep server up.
	} else {
		var theMessage messageFromChannelMasterToChatChannel
		theMessage.operation = fromChannelMasterToChatChanOpWho
		theMessage.userID = userID
		theMessage.userName = userName
		theMessage.doppelgangerID = doppelgangerID
		theMessage.doppelgangerCallback = doppelgangerCallback
		if runningChatchannelMap[chatChannelID] == nil {
			//
			// Should never happen.
			//
			logError("channel master error: runningChatchannelMap[chatChannelID] == nil")
			return // Try to keep server up.
		}
		runningChatchannelMap[chatChannelID].chatChannelCallback <- theMessage
	}
}

//
// DO IT
// Goroutine for channel master
//
func channelMasterGoroutine(incomingFromDoppelganger <-chan messageFromDoppelgangerToChannelMaster, incomingFromChatChannel <-chan messageFromChatChannelToChannelMaster, incomingHeartbeat <-chan bool) {
	//
	// We start off with an empty list of "running" chat channels (channels
	// with users in them, presumably talking). Chat channel live in the
	// database until someone actually wants to chat on them. Chat channel
	// goroutines are launched when the number of users on the channel goes
	// from 0 to 1. When it goes from 1 to 0, the chat channel goroutine is
	// supposed to send a message here telling us it is shutting down, and
	// we remove that channel from the "running" table.
	//
	// We have to use a pointer here to get arround the "cannot assign
	// to struct field in map" error that would normally occur when we
	// try to set the go channel for sending messages to the chat
	// channel's goroutine.
	//
	runningChatchannelMap := make(map[int64]*perChatChanInfo)
	for {
		select {
		case theMessage, ok := <-incomingFromDoppelganger:
			if !ok {
				//
				// Whoa, channel closed! Should never happen! Bail!
				//
				logError("channel master error: Channel master's channel for receiving messages from doppelganger's unexpectedly closed.")
				return
			}
			switch theMessage.operation {
			case fromDoppelgangerToChannelMasterOpJoin:
				chatChannelName := theMessage.parameter
				if chatChannelName == "" {
					//
					// No chat channel name.
					//
					var reply messageFromChannelMasterToDoppelganger
					reply.operation = fromChannelMasterToDoppelgangerOpJoinDenied
					reply.msgToUser = "Please specify a channel name."
					reply.channelID = 0
					if theMessage.doppelgangerCallbackFromChannelMaster == nil {
						//
						// Should never happen.
						//
						logError("channel master error: theMessage.doppelgangerCallbackFromChannelMaster == nil")
						return // Try and keep server up
					}
					theMessage.doppelgangerCallbackFromChannelMaster <- reply
				} else {
					chatChannelID, err := getChatchannelID(chatChannelName)
					if err != nil {
						//
						// Could not get chat channel ID -- db error.
						//
						log.Println(err)
						var reply messageFromChannelMasterToDoppelganger
						reply.operation = fromChannelMasterToDoppelgangerOpJoinDenied
						reply.msgToUser = "A database error has occurred."
						reply.channelID = 0
						if theMessage.doppelgangerCallbackFromChannelMaster == nil {
							//
							// Should never happen.
							//
							logError("channel master error: theMessage.doppelgangerCallbackFromChannelMaster == nil")
							return // Try and keep server up
						}
						theMessage.doppelgangerCallbackFromChannelMaster <- reply
					}
					if chatChannelID == 0 {
						//
						// User provided a name, but chat channel does not exist.
						//
						var reply messageFromChannelMasterToDoppelganger
						reply.operation = fromChannelMasterToDoppelgangerOpJoinDenied
						reply.msgToUser = "Channel #" + chatChannelName + " does not exist."
						reply.channelID = 0
						if theMessage.doppelgangerCallbackFromChannelMaster == nil {
							//
							// Should never happen.
							//
							logError("channel master error: theMessage.doppelgangerCallbackFromChannelMaster == nil")
							return // Try and keep server up
						}
						theMessage.doppelgangerCallbackFromChannelMaster <- reply
					} else {
						//
						// Join the chat channel!
						//
						joinChatChannel(runningChatchannelMap, theMessage.userID, theMessage.userName, theMessage.doppelgangerID, chatChannelID, chatChannelName, theMessage.doppelgangerCallbackFromChatChannel)
					}
				}
			case fromDoppelgangerToChannelMasterOpWho:
				whoIsOnChatChannel(runningChatchannelMap, theMessage.userID, theMessage.userName, theMessage.doppelgangerID, theMessage.chatChannelID, theMessage.doppelgangerCallbackFromChatChannel)
			case fromDoppelgangerToChannelMasterOpExit:
				if theMessage.chatChannelID == 0 {
					//
					// User asked to exit, but isn't on any channel!!
					//
					var reply messageFromChannelMasterToDoppelganger
					reply.operation = fromChannelMasterToDoppelgangerOpGenericText
					reply.msgToUser = "You are not on a channel. You have to join a channel before you can exit it."
					reply.channelID = 0
					if theMessage.doppelgangerCallbackFromChannelMaster == nil {
						//
						// Should never happen.
						//
						logError("channel master: theMessage.doppelgangerCallbackFromChannelMaster == nil")
						return // Try and keep server up
					}
					theMessage.doppelgangerCallbackFromChannelMaster <- reply
				} else {
					_, exists := runningChatchannelMap[theMessage.chatChannelID]
					if !exists {
						//
						// Should never happen.
						//
						logError("channel master error: runningChatchannelMap[theMessage.chatChannelID] does not exist (trying to exit a channel that doesn't exist)")
						return // Try and keep server up
					} else {
						var exitMessage messageFromChannelMasterToChatChannel
						exitMessage.operation = fromChannelMasterToChatChanOpExit
						exitMessage.userID = theMessage.userID
						exitMessage.userName = theMessage.userName
						exitMessage.doppelgangerID = theMessage.doppelgangerID
						exitMessage.doppelgangerCallback = theMessage.doppelgangerCallbackFromChatChannel
						runningChatchannelMap[theMessage.chatChannelID].chatChannelCallback <- exitMessage
						runningChatchannelMap[theMessage.chatChannelID].memberCount--
						if runningChatchannelMap[theMessage.chatChannelID].memberCount == 0 {
							var shutdownMessage messageFromChannelMasterToChatChannel
							shutdownMessage.operation = fromChannelMasterToChatChanOpShutdown
							shutdownMessage.userID = theMessage.userID
							shutdownMessage.userName = theMessage.userName
							shutdownMessage.doppelgangerID = theMessage.doppelgangerID
							shutdownMessage.doppelgangerCallback = theMessage.doppelgangerCallbackFromChatChannel
							runningChatchannelMap[theMessage.chatChannelID].chatChannelCallback <- shutdownMessage
							//
							// We do NOT close the go channel here -- we've
							// assigned responsibility for closing the go
							// channel to the other end (the chat channel
							// goroutine). The go channel is released for
							// the garbage collector.
							//
							delete(runningChatchannelMap, theMessage.chatChannelID)
						}
					}
				}
			default:
				//
				// Should never happen.
				//
				logError("channel master error: Unrecognized operation code received by channelmaster from doppelganger: " + intToStr(theMessage.operation))
			}
		case theMessage, ok := <-incomingFromChatChannel:
			if !ok {
				//
				// Should never happen.
				//
				logError("channel master error: incomingFromChatChannel go channel unexpectedly closed")
				return // Try and keep server up
			}
			switch theMessage.operation {
			case fromChatChannelToChannelMasterOpJoinDenied:
				runningChatchannelMap[theMessage.chatChannelID].memberCount--
				if runningChatchannelMap[theMessage.chatChannelID].memberCount == 0 {
					//
					// This code can't be an exact copy of the above code
					// because we're dealing with a message from the chat
					// channel instead of a message from the doppelganger.
					// But conceptually this does the same thing.
					//
					var shutdownMessage messageFromChannelMasterToChatChannel
					shutdownMessage.operation = fromChannelMasterToChatChanOpShutdown
					shutdownMessage.userID = theMessage.userID
					shutdownMessage.userName = ""
					shutdownMessage.doppelgangerID = theMessage.doppelgangerID
					shutdownMessage.doppelgangerCallback = nil // have to use nil as this field does not exist here
					runningChatchannelMap[theMessage.chatChannelID].chatChannelCallback <- shutdownMessage
					//
					// We do NOT close the go channel here -- we've assigned
					// responsibility for closing the go channel to the
					// other end (the chat channel goroutine). Go channel
					// is released for the garbage collector.
					//
					delete(runningChatchannelMap, theMessage.chatChannelID)
				}
			default:
				//
				// Should never happen.
				//
				logError("channel master error: Unrecognized operation code received by channelmaster from chat channel: " + intToStr(theMessage.operation))
			}
		case _, ok := <-incomingHeartbeat:
			if !ok {
				log.Println("Channel master error: Heartbeat channel closed.")
			} else {
				fmt.Println(timeNow() + " Active channels (channel master): " + intToStr(len(runningChatchannelMap)))
			}
		}
	}
}
