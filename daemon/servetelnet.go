package main

import (
	"github.com/reiver/go-oi"
	"go-telnet-mod"
)

func (handler chatHandler) ServeTELNET(ctx telnet.Context, writer telnet.Writer, reader telnet.Reader) {
	//
	// This is the starting point for when a new user connects to the system!
	//
	var userGoChannel chan byte
	//
	// NO buffer here because there is no cycle between the Telnet server
	// handler and the doppelganger goroutines.
	//
	userGoChannel = make(chan byte)
	//
	// Launch doppelganger.
	//
	go doppelgangerGoroutine(writer, userGoChannel)
	//
	// We are following the system that the creator of go-telnet (Charles
	// Iliya Krempeaux) used -- we create a 1-byte buffer and read bytes in
	// 1 at a time. This does not cause backspace characters to show up --
	// apparently the telnet client at the other end cleans up stuff like
	// backspaces before it sends the bytes across the network. Brrrrrp!
	// That's not true any more. It's true in half duplex mode, but we're
	// switching telnet to full duplex now, and now we get the backspace
	// characters in real time.
	//
	// Seems like the length of the buffer needs to be 1 byte, otherwise will
	// have to wait for buffer to fill up.
	//
	var buffer [1]byte
	p := buffer[:]
	//
	// This is the main loop of our telnet handler. Originally we tried to
	// process commands here, but since the switch to full duplex, we now
	// just toss off bytes to the doppelganger goroutine as they come in.
	// We have no way of getting a message back from the doppelganger, so
	// we also check for ^C and ^D characters here and exit if we see them.
	// That's literally all we do here. All the rest of the command
	// interpretation happens over in the doppelganger goroutine.
	//
	for {
		//
		// Read 1 byte.
		//
		n, err := reader.Read(p)
		if err != nil {
			//
			// Whoa, network connection is corrupted.
			//
			if userGoChannel == nil {
				//
				// Should never happen.
				//
				logError("Telnet goroutine: userGoChannel == nil")
			}
			close(userGoChannel)
			return
		}
		if n > 1 {
			//
			// Should never happen.
			//
			logError("Telnet goroutine: buffer should hold only 1 byte but n > 1")
		}
		if n == 1 {
			userGoChannel <- p[0]
			if (p[0] == 3) || (p[0] == 4) { // user typed ^C or ^D
				//
				// Be nice and put "Connection closed by foreign host" message
				// on new line.
				//
				_, err = oi.LongWrite(writer, []byte("\r\n"))
				return
			}
		}
	}
}
