package main

import (
	"database/sql"
)

// ----------------------------------------------------------------
// Begin message format definitions
// ----------------------------------------------------------------

// ----------------------------------------------------------------
//
// channel master -> user (doppelganger)
//
// ----------------------------------------------------------------

//
// Codes for responses from the channel master.
// Join denied is the same as generic text message but signals the doppelganger to set the chat channel id  back to 0.
//

const (
	fromChannelMasterToDoppelgangerOpGenericText = iota
	fromChannelMasterToDoppelgangerOpJoinDenied
)

//
// Format of the messages from the channel master to user (doppelganger) goroutines.
//

type messageFromChannelMasterToDoppelganger struct {
	channelID int64
	operation int
	msgToUser string
}

// ----------------------------------------------------------------
//
// user (doppelganger) -> chat channel
//
// ----------------------------------------------------------------

//
// Operation codes to send from users (doppelgangers) to chat channels.
//

const (
	fromDoppelgangerToChatChannelOpTextMessage = iota
	fromDoppelgangerToChatChannelOpExit
)

//
// Format of the messages from users (doppelgangers) to the chat channel
// goroutines.
//

type messageFromDoppelgangerToChatChannel struct {
	operation int
	userID    int64
	parameter string
}

// ----------------------------------------------------------------
//
// chat channel -> user (doppelganger)
//
// ----------------------------------------------------------------

// Operation codes to send from chat channels to users (doppelgangers).

const (
	fromChatChannelToDoppelgangerOpJoinDenied = iota
	fromChatChannelToDoppelgangerOpJoined
	fromChatChannelToDoppelgangerOpTextMessage
	fromChatChannelToDoppelgangerOpTextExit
)

//
// Format of the messages from the chat channel to the users (doppelgangers)
// -- including the actual chatting.
//

type messageFromChatChannelToDoppelganger struct {
	operation             int
	originator            int64
	chatChannelID         int64
	leavingDoppelgangerID int64
	parameter             string
	chatChannelCallback   chan messageFromDoppelgangerToChatChannel
}

// ----------------------------------------------------------------
//
// channel master -> chat channel
//
// ----------------------------------------------------------------

//
// Codes for messages from channel master to chatchannel goroutines.
//

const (
	fromChannelMasterToChatChanOpJoin = iota
	fromChannelMasterToChatChanOpWho
	fromChannelMasterToChatChanOpExit
	fromChannelMasterToChatChanOpShutdown
)

//
// Format of messages from channel master to chat channel goroutines
//

type messageFromChannelMasterToChatChannel struct {
	operation            int
	userID               int64
	userName             string
	doppelgangerID       int64
	doppelgangerCallback chan messageFromChatChannelToDoppelganger
}

// ----------------------------------------------------------------
//
// user (doppelganger) -> channel master
//
// ----------------------------------------------------------------

//
// Operation codes to send to the channel master, i.e. join a channel, exit
// a channel.
//

const (
	fromDoppelgangerToChannelMasterOpJoin = iota
	fromDoppelgangerToChannelMasterOpWho
	fromDoppelgangerToChannelMasterOpExit
)

//
// Format of the channel master message, used to make requests of the channel
// master, i.e. join a channel. Replies will use the response format above, and
// the request includes the channel to reply on. Channel master doesn't remember
// reply channels from call to call.
//

type messageFromDoppelgangerToChannelMaster struct {
	operation                             int
	userID                                int64
	userName                              string
	doppelgangerID                        int64
	chatChannelID                         int64
	parameter                             string
	doppelgangerCallbackFromChannelMaster chan messageFromChannelMasterToDoppelganger
	doppelgangerCallbackFromChatChannel   chan messageFromChatChannelToDoppelganger
}

// ----------------------------------------------------------------
//
// chat channel -> channel master
//
// ----------------------------------------------------------------

//
// Operation codes to send to the channel master -- there is only one, which
// is to tell the channel master a join failed. The channel master needs to
// know this otherwise it will have the number of members in the chat channel
// off by 1.
//

const (
	fromChatChannelToChannelMasterOpJoinDenied = iota
)

type messageFromChatChannelToChannelMaster struct {
	operation      int
	userID         int64
	doppelgangerID int64
	chatChannelID  int64
}

// ----------------------------------------------------------------
// End of message format definitions
// ----------------------------------------------------------------

//
// We attach our Telnet handler goroutine to respond to incoming user
// connections to this data structure, which is empty but has to exist.
// Defined here because it is shared between ServeTELNET and main.
//
type chatHandler struct{}

//
// Globals are a bad idea generally, but sometimes we need them. We strive to
// minimize use of them but not necessarily to zero.
//
// Putting all globals in a struct called "global" serves to make it explicit
// when we are using global variables.
//
// Here we use globals for connections to things where there's only 1 in the
// system -- only one database and only one channel master.
//
var global struct {
	db                               *sql.DB
	chanMasterFromDoppelgangerGoChan chan messageFromDoppelgangerToChannelMaster
	chanMasterFromChatChannelGoChan  chan messageFromChatChannelToChannelMaster
	chanMasterHeartbeat              chan bool
}
