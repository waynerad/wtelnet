package main

import (
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"go-telnet-mod"
	"log"
	"os"
	"strings"
	"time"
)

func createDatabase(dbFilePath string) error {
	db, err := sql.Open("sqlite3", dbFilePath)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	cmd := "CREATE TABLE user (userid INTEGER PRIMARY KEY AUTOINCREMENT, username VARCHAR(255) NOT NULL UNIQUE, password VARCHAR(255) NOT NULL);"
	stmtCreate, err := tx.Prepare(cmd)
	if err != nil {
		return err
	}
	_, err = stmtCreate.Exec()
	if err != nil {
		return err
	}
	cmd = "CREATE INDEX idx_usr_nm ON user (username);"
	stmtIndex, err := tx.Prepare(cmd)
	if err != nil {
		return err
	}
	_, err = stmtIndex.Exec()
	if err != nil {
		return err
	}
	cmd = "CREATE TABLE channel (channelid INTEGER PRIMARY KEY AUTOINCREMENT, channelname VARCHAR(255) NOT NULL UNIQUE);"
	stmtCreate, err = tx.Prepare(cmd)
	if err != nil {
		return err
	}
	_, err = stmtCreate.Exec()
	if err != nil {
		return err
	}
	cmd = "CREATE INDEX idx_chan ON channel (channelname);"
	stmtIndex, err = tx.Prepare(cmd)
	if err != nil {
		return err
	}
	_, err = stmtIndex.Exec()
	if err != nil {
		return err
	}
	err = tx.Commit()
	if err != nil {
		return err
	}
	return nil
}

func openDatabase(dbFilePath string) (*sql.DB, error) {
	exists, err := fileExists(dbFilePath)
	if err != nil {
		return nil, err
	}
	if !exists {
		err = createDatabase(dbFilePath)
		if err != nil {
			return nil, err
		}
		fmt.Println("Database created.") // Only happens once.
	}
	db, err := sql.Open("sqlite3", dbFilePath)
	return db, err
}

func fileExists(filepath string) (bool, error) {
	fhFile, err := os.Open(filepath)
	if err != nil {
		theMessage := err.Error()
		if theMessage[len(theMessage)-25:] == "no such file or directory" {
			return false, nil
		}
		return false, err
	}
	err = fhFile.Close()
	if err != nil {
		return false, err
	}
	return true, nil
}

func main() {
	//
	// Step 1, connect to our database. Create it if it doesn't exist.
	//
	var err error
	global.db, err = openDatabase("waynetelnet.db")
	if err != nil {
		log.Println(err)
		log.Println("Not starting server: Problem starting database.")
		return
	}

	//
	// Step 2, create channelMaster goroutine, the master goroutine for
	// coordinating joining channels. But first we create the channel other
	// goroutines will use to talk with the channelMaster. This has to be a
	// global (one of our very few globals) so the telnet handler that
	// answers incomming connections, and its doppelganger, will be able
	// to grab it and talk to the channelMaster.
	//
	// Buffer here needs to be big enough for all the users simultaneously
	// on the system -- if you know typical usage is lower, you can lower
	// the buffer size here.
	//
	global.chanMasterFromDoppelgangerGoChan = make(chan messageFromDoppelgangerToChannelMaster, 16384)
	//
	// Buffer here needs to be big enough for all the channels that will be
	// simultaneously in use -- if you know typical usage is lower, you can
	// lower the buffer size here.
	//
	global.chanMasterFromChatChannelGoChan = make(chan messageFromChatChannelToChannelMaster, 512)
	//
	// NO buffer here because there is no cycle between the "heartbeat"
	// goroutine and the channel master goroutine.
	//
	global.chanMasterHeartbeat = make(chan bool)
	go heartbeatGoroutine()
	//
	// Note that the channel for sending messages from chat channel
	// goroutines to the channel master is NOT a global. We have no choice
	// but to make the channel for messages from the doppelgangers global
	// because we have no control over parameters to goroutines launched by
	// the telnet package in response to incoming user connections, so we
	// have to use a global to get that channel to them (or the
	// doppelgangers they spawn). But this situation is not the case for
	// the chat channel goroutines we launch ourselves, where we have total
	// control. So, in the interest of minimizing globals, we don't make
	// that channel global.
	//
	// Launch channelMaster.
	//
	go channelMasterGoroutine(global.chanMasterFromDoppelgangerGoChan, global.chanMasterFromChatChannelGoChan, global.chanMasterHeartbeat)
	//
	// Step 3, open our port to listen to incoming Telnet connections.
	//
	var handler chatHandler

	keepGoing := true
	for keepGoing {
		keepGoing = false
		err = telnet.ListenAndServe(":5555", handler)
		if nil != err {
			//
			// The exact text of the error message is:
			// "accept tcp [::]:5555: accept: too many open files"
			//
			ii := strings.Index(err.Error(), "too many open files")
			if ii > 0 {
				keepGoing = true
				log.Println("We got the accept tcp [::]:5555: accept: too many open files error!!! Trying to keep going!!!")
				time.Sleep(10 * time.Second)
			} else {
				log.Println(err)
			}
		}
	}
}
