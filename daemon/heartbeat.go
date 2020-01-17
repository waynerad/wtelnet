package main

import (
	"fmt"
	"runtime"
	"time"
)

func heartbeatGoroutine() {
	for {
		count := runtime.NumGoroutine()
		//
		// We subtract 5 because that's our "baseline" -- the number of
		// goroutines running when no user has connected.
		//
		// 2 - SQLite database goroutines
		// 1 - the Telnet listener that listens for users connecting on the input port
		// 1 - the channel master goroutine
		// 1 - this heartbeat goroutine
		//
		time.Sleep(1 * time.Second)
		global.chanMasterHeartbeat <- true
		fmt.Println(timeNow()+" Goroutines running (heartbeat):", count-5)
		time.Sleep(1 * time.Second)
	}
}
