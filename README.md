# wtelnet
Telnet chat server that can handle up to about 3,000 simultaneous users


## Quick Demo

You can see a quick demo of the program, running on my Digital Ocean server and
can be accessed with the command below (provided your local system has the telnet
command -- if you're on a Mac and don't have it, see instructions for
installing it in the build instructions section):

```
telnet 157.230.235.202 5555
```



I wrote this program to make a Telnet program that could scale to a million
users. And, I'll just go ahead and skip straight ahead to the punch line here:
I wasn't able to pull it off. I was able to get a system that scaled to a little
over 3,000 simultaneous users -- not real humans but users simulated by a simple
(possibly too simple, as I'll explain below) test program with an average "think
time" of 10 seconds between sending messages. This was on a dual core AMD Athlon
II X2B24 with 8 GB of memory running Linux Mint 19.2 with Linux kernel 4.15.
What would happen is I got a "connection reset by peer" error, and I got it
robust enough to where altohugh the test client would crash (and slam all the
connections closed all at once) the server would stay up.

I never determined the cause of the "connection reset by peer" error. Maybe when
you have enough connections throwing enough messages through the same port all
at once, somewhere in the bowels of the OS (Linux, and I tested on Mac as well
and the same thing happened), the system gets overwhelmed and confused and
throws that error. One possible issue is that my test program was so simple, it
would send messages on connections without first bothering to pull all the bytes
sent to it out -- that would require creating another goroutine in the test
program (because network reads are blocking), and maybe this was a side effect
of bytes piling up inside the system. The official documentation for this error
says that it happens when one end of a TCP connection sends bytes on a
connection that the other end believes has either already been closed or never
existed. I couldn't see how my code could create that condition.

So, we've got a Telnet chat server that can
handle up to about 3,000 simultaneous users, on a reasonably average box for
these days. 
If the ultimate cause of the "connection reset by peer" error
could never be figured out, then it could be the case that the only solution
would be to make a multi-server design, which would require redesigning and
rebuilding the whole system. 
Part of the problem could be that I had pulled the "1 million"
figure off the top of my head without really thinking about what it meant.
Without thinking, for example, that if I wrote a test program that simulated 1
user logging in every 1 second, it would take over 11 days for all 1 million
users to log in. That's just to log in, not even to actually send messages
through the system. 1 million, as it turns out, is a lot.



## Design Philosophy

The starting point was that all data, if possible, should have a goroutine that
"owns" it and that serializes access to it. Nothing should be global, so no
explicit memory locks should be needed. (There are some languages, like Rust,
where you can explicitly indicate who "owns" a data structure, but this is not
part of the Go language. In this program, we do it by convention.)

A design where all pieces of data have a designated goroutine that is designated
as the "owner" of the data, and all reads and writes (especially writes -- reads
may be allowable as long as the data read is not subsequently used for writes,
such as reading a value, incrementing it, and writing it back) are funneled
through the "owner" and serialized in the process, then race conditions are
prevented. (Unfortunately not deadlocks as noted below.) If another goroutine
wants to modify a piece of data, it must send a request to do the operation over
a channel to the owner. It is forbidden to change data directly. Preventing race
conditions is incredibly important because bugs involving race conditions are
not reproducable.

Is this less efficient that writing "true" multithreaded code with locks around
critical sections? Yes, but computers are fast enough now that we can afford
slightly less efficient code for a stronger guarantee of reliability.

The next consideration was that each channel should have its own goroutine, so
there would not be a single goroutine that becomes a bottleneck. Of course,
there need to be data structures that keep track of what channels are running,
and since the philosophy is "all data should have a goroutine that owns it and
serializes it", a goroutine was created to keep track of that channel data. That
goroutines is called the "channel master".

Actual conversation between users, on the other hand, flows through the
goroutines for that channel, and all those goroutines can run in parallel, so
once channels have been created and users have joined their favorite channels,
and are chit-chatting with each other, there are no bottlenecks which slow the
system down. The system is maximally parallel.

At this point I need to interject a bit of terminology. Because the Go
programming language uses "channels" and the program we are creating also has
the concept of "channels", I decided to define specific terminology to avoid
confusing the two: Go language channels are always referred to as "go channels"
and channels representing the concept of "channels" in the chat program are
always referred to as "chat channels". The word "channel" is never used without
preceeding it with either "go" or "chat" to disambiguate. This convention is
used both in this document and in the source code.

At this point I have to bring up the difficulty of the Telnet package I chose.
I figured rather than code up the Telnet protocol from scratch, there was probably
a Go package out there already that did it, and I immediately Googled and found
"go-telnet" by "reiver" (Charles Iliya Krempeaux in Vancouver) at
https://github.com/reiver/go-telnet . Unfortunately, while I have never had
trouble with a Go package that I pulled off the net before, and all have been of
high quality, that was not the case this time.

Specifically, the issue with the go-telnet library was that it suppressed all
the commands in the Telnet protocol that follow the Interpret as command (IAC)
character. You see, in the Telnet protocol, the connection initially starts out
in "half duplex", which means your local system lets you type a whole line, and
then when you've got it finished (including fixing whatever mistakes you made
with the backspace key), the whole line is sent to the server all at once.

The problem with this is that in the middle of you typing a line, if you are on
a chat channel talking with someone, *they* might interrupt your typing with a
message. In half-duplex mode this rudely shoves someone else's message in the
middle of your line (which can be preceeded by a line break to confuse you less),
and leaves you to finish typing your line with what you typed earlier still
sitting there in front of their line.

I reluctantly accepted there was no possible solution to this in half-duplex
mode, and that I had to switch the system to full-duplex mode. In full-duplex
mode, every single keystroke you type gets immediately sent to the server as its
own packet, and the server has to handle all the interpretation of the
keystrokes, including backspaces. But that wasn't the only problem. The
go-telnet package made it impossible to change the system from half-duplex mode
to full-duplex mode, because it was stripping out the IAC character and commands.
In order to get the system to work, I had to modify the go-telnet package to
allow the IAC and commands to go through, at least for bytes sent from the server
to the client. (IAC characters and commands from the client to the server are
still stripped out.) This is why, instead of doing "go get
github.com/reiver/go-telnet", you have to use "go-telnet-mod", the modified
version of go-telnet that I'm providing in this submission. (You still have to
"go get github.com/reiver/go-oi", where "oi" is "io" spelled backwards and is a
whacky package of I/O helper functions used by go-telnet, and which I also used,
especially the "LongWrite" function, more on that below.)

As to why the go-telnet package has this problem, I don't know, but I'll put
forward a guess: the person who created it had a specific use case in mind, one
where you telnet to a server and give commands that are executed by the server.
Your commands aren't interrupted by unpredicable output from the server, say,
originating with another user in the real world that you are chatting with.
Unfortunately, people like me might think it actually fully supports everything
in the Telnet protocol, even though that's not actually the case.

Anyway, at this point you might think we've got our full repertoire of
goroutines, but you'd be wrong. There's the remaining problem of the goroutine
that picks up user input from the Telnet package blocking on network reads. It
can't simultaneously send out messages from other users who are chatting and
receive input if it's blocked.

It's possible there are other ways to do it, such as enabling the chat channel
goroutines to directly write to the network and send messages to users, which
would involves their own considerations such as how to make sure they never
tried to write on a network connection after it was closed, but I wanted one
goroutine responsible for all the input and output going to and from one user
in the real world.

The solution I came up with was, instead of having one goroutine handling direct
interactions with each user, I'd have *two*. I called the second one the
"doppelganger", intending to rename it to something more sensible later on. But
in the end, I never thought of a better name. While "doppelganger" doesn't make
sense at first glance, once you know what it means, because it's such a distinct
term, it's impossible to confuse with anything else in the system, and I found
myself unable to think of a better term. (If you think of one, let me know.)

In my original formulation of the system, I had the goroutine spawned by the
go-telnet protocol that was listening on the port every time a new user
connected, which I will henceforce refer to as the Telnet goroutine, processing
commands from the user (such as commands to join a channel, etc). After the
switch to full duplex, I moved all this functionality to the doppelganger. The
Telnet goroutine literally now just accepts bytes on the telnet connection and
passes them on to the doppelganger. Because the doppelganger has a "select"
statement that receives messages on multiple channels, it is able to do other
things while waiting for the user to type a character. One of those things is to
receive messages typed by other users, and to send those down to its user in
such a way that doesn't completely mess up the user experience like the
half-duplex mode does. The way it achieves this is by "backspacing out" what the
user has typed, outputting the message from the other user, and then re-
outputting what the user has typed so far. It sounds whacky but the end result
is the conversation scrolling up the screen, looking like a transcript of the
conversation without any unnatural breaks (in fact looks very similar to what
ends up in the log file for the channel except without the time codes).

Ok, at this point, we can lay out the repertoire of goroutines for the system:

- chat channel -- The goroutine that maintains all the state of a channel (such as
    its name and member list) and that propagates messages from one user to
    everyone else on the channel.

- channel master -- The goroutine that maintains the list of active channels, that
    launches goroutines for channels and tells goroutines for channels when to
    shut down.

- Telnet -- To the goroutines that receives keystrokes from users, 1 per user.

- doppelganger -- The goroutines that runs alongside the Telnet goroutine, but
    does all the real work -- processing commands from the user (such as
    commands to join a chat channel) and picking up messages from other users on
    the chat channel and formatting them in a palatable way for the end user.


At this point you ought to be able to look at the source code file names and guess what some of them mean.

- chatchannel.go -- The code for the chat channel goroutine.

- channelmaster.go -- The code for the channelmaster goroutine.

- servetelnet.go -- The code for the Telnet goroutine.

- doppelganger.go -- The code for the doppelganger goroutine.


In addition there are a few other files:

- datastructures.go -- This file defines all the messages that are sent through
    all the go channels that are used for communication between all the
    goroutines. All the message structures have a name like "messageFromXToY",
    which, even though a bit tedious, makes it very unambiguous how the message
    is intended to be used. Most messages start with an "op code" that enables
    multiple (similar) messages to be sent through the same go channel, rather
    than creating separate go channels -- no point in having multiple go
    channels between the same goroutines. These op codes are constants with
    names of the format "fromXToYOpZ", which, although a bit tedious, is
    unambiguous. In addition, throughout the source code, variables for outgoing
    go channels (go channels that send messages from the current goroutine to a
    different one) start with "callback" (though some of them are technically
    not callbacks -- but the majority are so the prefix works) and variables for
    incoming go channels start with "incomingFrom".

- heartbeat.go -- This file defines a "heartbeat" goroutine which is helpful for
    periodically outputting the number of active goroutines and number of active
    channels. I originally planned to put this in for debugging purposes only,
    and then take it out of the final version. But in the end I decided to leave
    it in, as it enables monitoring of the system while it's running, and seemed
    to impose little cost for doing so, and if the system ever needs to be
    debugged again, I'd just be putting it back in. Note: the number of
    goroutines reported is actually the number minus 5 as a baseline (2 SQLite
    database goroutines, the Telnet listener, the channel master, and the
    heartbeat goroutine itself), but sometimes the baseline is different (I
    think because of SQLite -- which seems to always start more goroutines when
    the DB is first created on the initial run) and you get different numbers.

- helper.go -- Some simple helper functions for things like string conversions.

- wtelnet.go -- This is the file that has the main() function that launches the
    whole thing. The main function turns into the goroutine that listens for new
    connections, and normally stays running "forever". This file has the
    intended name of the compiled executable, "wtelnet" (Wayne Brain Telnet
    daemon).

A few additional notes on the design:

- The process of joining a channel is: user (doppelganger) sends a message to the
channel master asking to join the channel, passing along a callback channel,
which the channel master gives to the chat channel goroutine. We could have done
it a different way, where the users (doppelgangers) asked the channel master for
a go channel to the chat channel goroutine, and then sent its request to join
directly to the chat channel goroutine. In case you're wondering why it's done
that way instead of the other way, I felt these were more or less equivalent in
complexity, "six of one, half a dozen of the other", and that the choice to do
it the way it was done was arbitrary.

- A few final things to note about the design, before we move on. The doppelganger
can only write to the network. The Telnet server handler can only read. If you
look at the handler function, you'll see that the *only* thing it uses the
writer for is to hand it off to the doppelganger, which it launches when it
first starts. The doppelganger is *not* given a copy of the reader. This is by
design to ensure a clean separation of concerns.

- As noted above, all data would be "owned" by a goroutine. In order to make the
state of each goroutine accessible to all the functions within that goroutine,
they are placed in a structure and a pointer to that structure is passed to
functions that are part of that goroutine, when applicable. This pointer is
never passed to any code not part of that goroutine or allowed to be placed in a
go channel.

- When I originally designed the system, I had the chat channels keep track of the
users in the channel by user ID, but I later changed it to keep track of
doppelganger IDs, and to allow the same user to log in more than once. This
makes it possible for the user to talk to themselves on the same chat channel.
That's not a normal use case, but I realized it would be perfectly normal for a
user to log into the system multiple times and chat with multiple people on
different channels, so I wanted to make sure it could handle the use case of the
same user logging in multiple times, and I decided that the best way to handle
the scenario where the user does join the same chat channel multiple times is to
just act like that's perfectly normal and let them talk to themselves.

- As a coding style rule, since the code has a lot of error handling, I followed
rule of putting "exceptional" cases before "normal" cases. Although a lot of the
error handling code looks redundant, I found it testing, in the doppelganger
goroutine main loop, where there are checks all over the place for LongWrite
failures that indicate the end user went away and closed the connection, under
sufficiently heavy load all of them will in fact get hit on the server. The
error checking code may look tedious but it does in fact make the server much
more robust.



## Architecture Overview

So to summarize: primary architectural decisions:

- All data "owned" by a goroutine, with reads and writes serialized by that
  goroutine.

- One goroutine per channel:
  - Goroutines are not launched until someone uses the channel.
  - Goroutine for channel serializes all the conversation in the channel and
    establishes the official "authoritative" version of the sequence of messages
    in the conversation.
	- Goroutine can be stopped when the last person leaves a channel.

- One "channel master" goroutine to prevent race conditions:
  - What if two users decide to join exactly the same channel at exactly the
    same moment? Could the "one goroutine per channel" rule be broken and we get
    two goroutines for the same channel? Better serialize all joins and exits to
    all channels to make that impossible. All joins and exits are serialized by
    going through the channel master goroutine, and there is only one channel
    master goroutine.

- One log file per channel:
  - If all channels logged to a single log file, then all messages on all
    channels would have to be serialized, and that single point of serialization
    would become a bottleneck under heavy load.
    - There is already one goroutine responsible for serializing the
      conversation for the channel -- easy to add logging properly serialized at
      that point.
    - If writing to many logfiles at once on disk becomes a bottleneck, the
      logfile for each channel can be moved to a separate physical device. Could
      be a problem if the number of channels isn't fixed in advance but users
      are allowed to make their own channels.
    - Having all the channels in the same log file doesn't make sense from a
      "human conversation" standpoint -- it would interweave many conversations
      together that a log analysis tool would have to separate out later on.
      Having each channel logged in its own file means the conversation can be
      analyzed just by reading the one file.
    - Log files for a channel are held open while the channel is in use, which
      could affect how they are rolled over, or how they are handled by server
      backup systems.
    - For long-term continuous usage, it might be a good idea to close the log
      file and start a new one, say once per day or per week. The date could be
      used to differentiate the file name.

- User and chat channel database as sqlite3
  - I'm a believer in the relational data model.
  - bcrypt provides industrial-strength hash encryption. (Note: without
    switching to TELNETS, the secure SSL-enabled version of the Telnet protocol,
    passwords are still sent across the network in cleartext.)



## Deadlocks

During development, I managed to deadlock the system. The way it happened was
something like this: a user types a message and the doppelganger sends it to the
chat channel goroutine for the chat channel the user is on, but if that chat
channel is busy distributing a message to everyone on the chat channel, it can't
pull the new message out of the go channel at that moment, and when it gets to
distributing a message to the first user, that doppelganger will never pull the
message out of the go channel because it is blocked waiting for the chat channel
goroutine to pull the message it already sent out of the go channel to the chat
channel goroutine.

This made me realize, even if you use a language like Go that uses the "channel"
metaphor, instead of writing code with critical sections and mutexes, and even
if you carefully design your data structures so that every data structure has a
goroutine that "owns" it and that serializes access to it, it is still possible
for the system to deadlock. In this case, one doppelganger and one chat channel
goroutine deadlock but everything else in the system keeps running, so the Go
runtime does not shut down -- if the whole system deadlocks, the Go runtime will
notice and take down the whole program.

The key to understanding whether your system can have deadlocks is to represent
all your goroutines as nodes on a graph, and all of the channels between them
as directed edges between nodes. If there are no cycles in the graph, you are
good -- your system can't have deadlocks. Having no cycles also makes the
shutdown sequence easy -- you just shut down the goroutes in the order they are
connected on the graph by the directed edges.

In the case of this program, the graph has cycles and this is hard to avoid --
the cycle comes into play when you realize that users have to be able to send
messages to a chat channel and have their message sent back to them. It is
possible to have users' own messages not sent back, but a special flag set to
tell the chat channel not to reflect those messages back, but I liked the idea
that all messages on a chat channel were handled in a uniform way -- including
the logging -- so that every user was guaranteed to see the same conversation
and the log was guaranteed to also record the same conversation. There should
be only one "official" authoritative version of the conversation. So I never
seriously considered doing it that way and instead took on the challenge of
solving the deadlock.

In the case of Go, you solve these deadlock issues by making buffered go
channels. With a buffered channel, instead of one goroutine blocking until
another can pull a message out of a go channel, it puts the message in the
buffer and continues on its way. The other goroutine will pull the message out
of the go channel when it's ready. Blocking only occurs when the buffer fills
up. This raises the question of how big the buffers need to be.

In analyzing the cycles in the graph, I determined that there needs to be a
buffer for at least 1 message for each cycle through the graph. What this means
is that I *think* you can eliminate cycles just by identifying one node in the
graph that has only one input, and put a buffer of 1 message on that go channel.
However, in the implementation of this program, I put buffers on all the
channels, equal to the number of inputs. Well, unfortunately for some channels,
the number of inputs is determined at runtime and is always changing, but the
buffer size still has to be determined at the moment the channel is created,
when the ultimate number of inputs (graph edges directed at it) is unknown. So
I just put in "reasonably large" constants. Like I said, altohugh I don't have a
mathematical proof of this, I *think* that having buffers of 1 somewhere in
every cycle where there is guaranteed to be only 1 input edge is sufficient to
prevent deadlocks. Because in the doppelganger-chat channel cycle, each
doppelganger is guaranteed to receive input from only 1 chat channel at a time,
a buffer size of 1 on that go channel should be sufficient. (End users can log
in multiple times, and talk on multiple chat channels, but each login will still
have its own doppelganger goroutine.) There are two other cycles in the system:
one between doppelgangers and the channel master, where the doppelganger is
guaranteed to have only one channel master input (because there is only one
channel master in the whole system), and one between chat channels and the
channel master, because chat channels need to be able to tell the channel master
that a join request was rejected, and in that cycle, the chat channel is
guaranteed to have only one channel master input (again because there is only
one channel master in the whole system).

In any case, in my efforts hammering the system with the test program, I was
never able to get the system to deadlock again, once I put the buffering scheme
in place. The buffering doesn't cause any memory issues -- in fact Go is so
efficient with memory that this program was always using much less memory than
other programs on the system, like the web browser. Likewise I was able to get
over 2,000 goroutines going and still was using less CPU than my idling web
browser (though it had 11 tabs open and we know some webpages gobble up CPU even
though they don't look like they're doing anything). At about 2,100 goroutines,
the chat server overtook the idling web browser.

The last remaining "concurrent programming" issue is that it is possible for
bits of data in different goroutines to get out of sync. One thing that's
possible is for a doppelganger to think it's on a chat channel and send it a
message to exit, but for the chat channel to think that doppelganger *isn't* on
the channel and to not reflect the exit message back. This can happen, for
example, if a join request is rejected by the channel but the end user
disconnects before the news of the join request rejection gets back to the
doppelganger and the doppelganger has already initiated its shutdown sequence.

Because of cycles in the communication graph, the shutdown sequence is
complicated. A doppelganger can't simply shut down if it is on a chat channel
because when that chat channel tries to send messages, it will crash the system
because it's trying to send a message on a closed channel. We're assuming here
that channels are closed before goroutines exit, which is a convention I tried
to follow throughout the system. If you close a go channel, it will cause the
program to crash if another goroutine tries to send on it, but the alternative
is worse, which is that if you don't close the channel, the sending goroutine
will block forever (or at least it could, once the buffer gets filled up).
Better to have the program panic and crash and then at least you know where
the problem happened, rather than have goroutines mysteriously hang forever.

Anyway, the arbitrary solution I came up with to the cycle between doppelgangers
and chat channels is to have the doppelganger wait until it has received
confirmation from the chat channel that that user (doppelganger) is off the chat
channel. In the case of the synchronization issue described above, this message
never arrives, and the doppelganger waits forever and never shuts down. The
solution I made for this is for the doppelganger to wait 1 second to allow time
for messages to propagate around the system, then try again, and keep a counter,
and once the counter reached 1,000, it would assume it was waiting forever for a
message that would never come and shut down anyway. In my testing it appeared
that this got all the orphaned doppelganger goroutines to shut down and for the
system to clean itself up, though it would take a few minutes. It doesn't
interfere with new users signing on because the system just launches new
doppelganger goroutines for them, and doesn't try to connect them up with the
zombies. The zombies eventually shut down and clean themselves up, so the system
continues functioning properly in an ongoing manner.

In more detail, the way this is handled is with two flags in the doppelganger,
telnetGoroutineHasGoneAway and cantExitBeforeExitMessageFromChannel. During
testing, a crash happened at the point where a message was being sent from the
channel to the doppelganger -- it turned out it was possible for the
doppelganger goroutine to disappear before the channel was aware of it, and the
channel could attempt to send a message to a nonexistent doppelganger.
Originally I had a flag, isUserGone, that was part of the messages
messageFromDoppelgangerToChannelMaster and messageFromChannelMasterToChatChannel,
and that flag was just passed around when the user was done, but upon realizing
that was inadaquate, I re-thought the whole problem and how to guarantee the
doppelganger goroutine would exist whenever the chat channel tried to send a
message to it (keep in mind the doppelganger goroutine can continue to exist
after the telnet connection has been dropped and the user has gone away). That
resulted in isUserGone being replaced by a pair of flags that are part of the
doppelganger's goroutine: telnetGoroutineHasGoneAway and
cantExitBeforeExitMessageFromChannel. This way, if the user has joined a chat
channel, and unexpectedly closes the connection, the doppelganger goroutine will
continue to exist until messages have made the round trip through the
channelmaster and the chat channel goroutines removing the user from that chat
channel. Once the user is completely out, then it is safe to shut down the
doppelganger goroutine because there can't be a chat channel goroutine with a
reference to a channel that communicates with it. But works but doesn't handle
the situation where things are out of sync in the other direction -- the chat
channel doesn't know to send the exit message back to the doppelganger. For
this, a counter, cantExitCount, was created that counts the number of times the
doppelganger loops through the cantExitBeforeExitMessageFromChannel loop and
once it reaches a certain threshold, it assumes the goroutine is a zombie and
exits.

Note that because there is no cycle between the Telnet goroutine and the
doppelganger goroutine -- messages go only from the Telnet goroutine to the
doppelganger, telling it about keystrokes from the user -- the shutdown
sequence is straightforward: the Telnet goroutines shuts down when the user
disconnects and the doppelganger follows suit and shuts down afterward.

There's other places in the system where I was concerned about data getting out
of sync, such as between the channel master and chat channels where the channel
master keeps a count of the number of users on each channel and it's possible
for these counts to be briefly out of sync with the member lists in the actual
chat channel goroutines themselves. However, I couldn't get any problems to
surface because of this during testing.

The channel master's count of users on a chat channel is incremented when a user
joins, which means the channel master won't try to release and shutdown the
channel, and then after that the user is added to the chat channel's roster of
who is on the channel. When the user leaves the channel, the message to take the
user off the roster is dispatched, then the channel master decrements the count,
and then, if the channel needs to be shut down, the shut down message is sent,
and is guaranteed to arrive *after* the message taking the user off the roster,
so the chat channel should be guararteed to also have 0 users at the time it
receives the shutdown message. This is really critical as a bug in the system
where a channel goroutine shuts down without all the users exiting first could
leave open go channels which will deadlock other goroutines when those other
goroutines try to send messages.

One final word about the testing: I like to be able to unit test functions and
other logical "subunits" of code. I try not to write anything without *some*
automated testing, though I find in practice being "religous" about test-driven-
development (TDD) is impractical -- lots of code, while testable in theory, is
simply not worth the effort of writing the automated test code for in practice
(it's not worth it if your test code is going to be 10x the size of your
production code, for example, unless it's extremely critical functionality).
However with concurrent goroutines, I found I didn't have a way of unit testing
them even in theory. Google searches on the topic were not helpful. So one of
the things I need to figure out how to do is isolate a goroutine and unit test
it, and I'll do some more extensive digging on Google because there's probable
some ideas out there somewhere. If you all have ways of isolating goroutines for
unit testing, please tell me what they are. In this system, for testing I just
wrote a program that (crudely) emulated end users, and just hammered away at the
whole system.

The way I debugged problems in the concurrent code was to whittle down the test
to the smallest test that would reproducte the error. For example I originally
set the limit of people in a chat channel to 6 but when it went over, the next
person wouldn't get allowed in and the system would go into an invalid state
where it would go into essentially an endless loop with the doppelganger
goroutine eating up all the CPU. Later on when I had a problem where I was
testing putting as many users as possible all on the same channel, I found the
system wouldn't unwind properly, and I reduced it until I could reproduce the
problem with only 4 users.



## User Interface

The system begins with a simple login system with ability to create a new
account. After that, commands are preceeded by a "/" in IRC style. I had a look
at IRC commands and mimicked them, but the commands here are not exactly the
same. The commands (available within the program by typing "/help") are:

- /list                 -- list channels
- /create <channelname> -- create a channel
- /join <channelname>   -- join a channel
- /who                  -- show who is on the current channel
- /exit                 -- exit the current channel

Once on a channel:
- /say   -- say something on the current channel
- /emote -- emote on current channel
- /think -- think something on current channel
- /sing  -- sing something on current channel

- /help  -- this command

Abbreviations:
- ' -- say
- ; -- emote

- ^D log off


The problem description said you're looking for "Creativity!" Well, one little
bit of creativity I threw in here was commands like "think" and "sing". Although
they won't be used often, when they are they should make the chat experience
more fun!

One quirk about this user interface is that you can't type blank lines. I
decided to do it that way to make the screen contain more of an "unbroken"
scrolling transcript of the conversation. Of course, it is still possible to
break it by typing things that requires the system to give you error messages.
But at least it won't be broken up by blank lines. The downside is that users
expect from other systems to be able to type blank lines to see if the system
is listening.

One other thing I did for creativity was put a limit on the number of people on
a channel. I've noticed in real life conversations, once the size of a group
gets to about 6 or more, it always seems to split into sub-conversations of 2 or
3 people. So I made 6 the limit to the number of people on a channel.

Note that although I said earlier I detest putting hard-coded limits in code,
this limit is part of the user interface. And in fact for testing purposes I
removed it, just to make sure the underlying code doesn't have any arbitrary
limit. In testing with all users on one channel I was able to get the number of
users up to 920.



## Database

Because SELECT for users (and channels) happens inside the transaction for
creating them, the database *should* prevent a race condition whereby it's
possible to have the same user account created more than once -- however because
many databases use row-level locking, and we're not actually changing the row
that already exists (we're creating a new one), it's possible for row-level
locking to fail to catch it. The "UNIQUE" constraint was added to the username
field (and channelname as well) so if the database system supports it, this race
condition will be prevented by disallowing duplicate entries in the table at the
database level.

If the database I used (SQLite3) is replaced with a different database system,
it needs to support the UNIQUE constraint. I did not serialize access to the
database through the channel master goroutine. I assumed the database itself
could handle concurrency issues. So user (doppelganger) goroutines are allowed
to modify the database, which currently consists only of adding users and
channels. (Users currently cannot be deleted or renamed, nor can channels be
deleted or renamed. Users can't change their usernames or passwords either. All
these functions would be needed if we were making a real, production system.)

The code is written to the standard SQL package interface in Go, so it should be
possible to swap out the database with a different database. If you use another
DB, make sure you declare IDs as 64-bit integers (e.g. BIGINTS in MySQL). User
IDs (and channel IDs) are declared as int64 so the system can have
9,223,372,036,854,775,807 users. Using 32-bit integers limits the number of
people to 2,147,483,647 (2.1 billion vs the world population of 7.6 billion) --
if you squeezed another bit out using unsigned integers, it's 4,294,967,295
(4.2 billion -- still less than the 7.6 billion world population). I've tried
using unsigned integers and think they're more trouble than they're worth unless
you really need them -- certain things you take for granted, like decrementing a
counter to 0, don't work when you use unsigned variables. 63 bits is enough for
user IDs; you don't really need that 64th bit. Having said all this it's
unlikely the entire world population will use the same Telnet server at the same
time.

I named tables in the singular: "user" and "channel". Many organizations use
plurals by convention (which would make these tables "users" and "channels").
There doesn't seem to be one "right" way to do it; you can make logical
arguments either way.



## LIMITATIONS AND KNOWN BUGS



### Limitations

1. The biggest limitation is that the system can only handle about ~3,000
simultaneous users before "connection reset by peer" errors start occurring.
After putting in some effort at figuring out what caused this, I decided to stop
trying to figure out the cause and send the code in that I have, due to time
considerations. It appered that determining the cause of this error was not
straightforward and could take a lot of time. Simple Google searches on the
error had failed to reveal anything enlightening. How much more effort would be
involved? The worst-case scenario would involve digging through the source code
of either the Go language or the Linux OS (both of which are open-source, so
that is possible).

2. Operating systems have a "file descriptors" limit, and open socket
connections consume a file descriptor in the underlying OS. The limit was put
in place for a sensible reason -- to keep buggy programs from running amok and
taking over all the file descriptors in the system. However for this program, it
imposes a limit on the number of simultaneous users. When this limit is hit, the
user gets an "Connection failed: Connection refused," error, and on the server's
end, the goroutine that listens for new connections on the port will get an
"accept: too many open files" error, however, it will catch the error and wait
for 10 seconds and start listening again. Any users who try to connect during
this time will get the standard "Connection refused" error. The 10 seconds is
arbitrary -- just wanted to allow some time for a few existing users to
disconnect.

3. It probably also counts as a "limitation" that the program is difficult to
compile on Windows (see details in the build section).



### Known Bugs

1. The biggest (and only) known bug is that there is a timing issue that makes
it possible for a user to disconnect before a chat channel join request has
fully gone through, and the system will try to take the user off a chat channel
they're not on before shutting down the goroutine that represents them (the
doppelganger goroutine) on the server, will never get the reply that the user is
off the chat channel, and will wait -- well, it won't wait forever. I did get it
so the goroutine will eventually recognize it's a zombie and shut down
gracefully. So the server doesn't crash and there aren't any memory leaks or
anything, but that was as robust as I was able to get it -- I wasn't able to
track down the original cause of the bug. A more general thing to note from this
is that, while my fundamental design of having every piece of data owned by a
goroutine that serializes access to it completely eliminates classic race
conditions -- such as where a value gets read from memory, incremented (say),
and written back, and the increment gets lost because another thread wrote
something to the same memory address at the same time -- it does not elimate
*all* timing problems.



## Miscellaneous Issues


This program has a lot of issues that don't quite rise to the level of being
called "bugs", but might be things you'd probably actually have to deal with in
a real production environment.

All characters are allowed in channel names, but not all characters are allowed
in file names. For example "-", "&", quote marks. Since the log file for each
channel is based on the channel name, this could be a problem if users create
channels with funky characters in them.

We allow spaces in channel names. This is probably not a good idea, and not just
because we end up with spaces in file names. We probably need a syntax for
putting quote marks around channel names. Or just disallow spaces. As a security
measure, we don't allow slashes in the chat channel log file names, even though
we do allow slashes in the actual channel names (which maybe is a bad idea).
Preventing slashes in the file names prevents hackers from hacking our server by
changing directories.

On the subject of channel names, I tried using unicode characters (Chinese
characters in my case), and amazingly enough, it worked! The people who
designed the UTF-8 unicode standard did an amazing job as they made a system
that enables unicode characters on ancient protocols designed long before the
invention of UTF-8. I fully expected it not to work and having to report it
*not* working as an issue here. However, if you use unicode characters for a
channel name, those unicode characters will get used for the filename, which,
depending on your file system and whatever other software you use on that files
system (software for backups, software for rolling log files, etc) may pose a
problem.

While on the subject of channel names, it should be noted that this system does
not have a "moderation" system, so there is nothing preventing users from
creating channel names with profanity or otherwise a problem. There's no "admin"
users who can create channels or have other special privileges. In a real
system, with millions of real people using it, you'd need some type of
moderation system.

The system logs conversations but there is no user interface to make the logs
available to users. Presumably users would rather not cut-and-paste
conversations but would like access to the transcripts the system makes, but we
don't provide any way to do this. The logging system, as its currently
implemented, can be used for analysis by data scientists on the backend, but
doesn't provide any value to users.

In half duplex mode, the Telnet client gives the user a "key" cursor when
they're typing their password. In full duplex mode, we don't get this. The
password is not echoed back by us simply not echoing it back.

The "/list" command just dumps a list of all the channels from the DB. The
command doesn't consult the channel master to see how many people are on each
channel. That information would be useful to know to know what channels people
are actually talking on, so a new user could join one of them to talk to
someone.

We force users to explicitly exit the channel they're on before joining a new
one. This prevents a possible race condition where two channels might think the
same user is on both channels at the same time, and spares us having to write
complicated messaging code asking the new channel if the user can join, then
dropping the current channel, then joining the new channel, all in response to
one "/join" command, but it makes for a cumbersome user interface.

There's a theoretical race condition where in between when you hit return on a
message and see your own message reflected back, another message from a
different user could arrive. This would mess up the screen formatting (but cause
no other problems as far as I know). The automated test client did not test for
this, and I never saw it in manual testing.

There are situations where, if there are multiple errors that happen at once,
the system probably does not do proper error handling. For example if a database
error happens and then in the handler for that a network error happens, the
network error doesn't get handled properly and goroutines associated with the
network connection don't get properly cleaned up. If this were to become a true
high-volume production system, these spots in the code would all need to be
tracked down and fixed up. (Although if you're getting that many errors, maybe
something is wrong in your production environment that needs to be addressed
anyway.)

My biggest worry is that it's possible for a network write to hang. This would
cause a doppelganger goroutine to hang, would would cause the channel to hang on
the next message to it, as well as cause the channel master goroutine to hang on
the next message to it. While this couldn't deadlock the entire system, it could
deadlock a channel and could deadlock the channel master, which is pretty
serious. Once a channel or the channel master goroutines are permanently blocked
in such a manner, I don't know any way to get them unblocked. You just have to
kill the whole process and restart it. Maybe this is a baseless worry, since it
never occurred in testing. One of the things that has affected another Go
program that I wrote it that it's possible for goroutines to hang forever, and
I've been meaning for some time to research how to set all the various network
timeouts in Go so that stuff will time out in a reasonable time frame and return
error codes, ensuring nothing in the server ever hangs forever and enabling it
to keep going and recover from the error.

I discovered with lsof I can see which connections are assigned to which
threads. I knew Go's goroutines don't correspond in a 1-to-1 manner with OS
threads, that the Go designers had come up with a way of making goroutines more
"lightweight" and put multiple goroutines on the same OS thread, but I never had
a way of actually seeing this before. There's probably a way to see it through
the runtime package, but I had a look through it and didn't see anything
obvious. And of course, lsof can't show you what thread a goroutine is using if
the goroutine doesn't open a socket or something else that uses a file
descriptor, so it's not a complete overview, or anything like that. Just
something that gave me a sense of how many goroutines Go puts on a single OS
thread -- in my case, it was a lot (like over 600).

Before I got it up over 3,000 users, in testing multiple users on different
channels, the server crashed with "accept tcp [::]:5555: accept4: too many open
files". This happened at 1,019 simultaneous users and 3,051 goroutines. The
reason for this turned out to be that on Unix-type systems (including both
Linux and Mac which uses FreeBSD under the hood), file descriptors in the OS are
used for open sockets. There's typically a limit to the number of file
descriptors a process is allowed to have, to prevent a runaway process from
taking over the system. I had a limit of 1,024 file descriptors.

Instructions for changing the number of file descriptors on a Linux system are
included in the build instructions section. Even then I didn't get "unlimited"
or the full 8,388,608 that I asked for. I got 1,048,576. So there's still
something in my system imposing a limit, but I don't know what. But with further
testing I ran into the "connection reset by peer" problem shortly after
crossing 3,200 limits and never got all the way up to 1,048,576. 1,048,576 would
have been enough anyway, since my goal was 1 million.



## Final Thoughts

This exercise made me learn a lot more about concurrent programming. Prior to
this, I had done only very simple concurrency in Go, for example starting
multiple goroutines that collect data at the same time (such as scanning
directory trees) and return it back to a primary goroutine when they are done.
It was actually quite a thrill seeing this system fire up thousands of
goroutines at the same time -- and see how much less memory and CPU impact this
had from what I would have expected. It is truly a testament to the skill of the
Go language designers how well the Go language handles this.

The biggest lesson I learned is, before you start writing a line of code, graph
out all your goroutines as a directed graph where the nodes represent goroutines
and the edges represent go channels, and try to eliminate all the cycles. 

I feel like I've built a piece of code that is useless in
the real world: nobody uses Telnet chat systems any more. So I started thinking
about what I could use this code for. The underlying ideas from it could be useful for a real-time (or near real
time) publish-and-subscribe system. Perhaps the fact that in such systems, the
"publisher" is never a "subscriber" and a "subcriber" is never a "publisher",
unlike this system where everyone on a chat channel is both a "publisher" and
"subscriber" simultaneously, could make it possible to graph out the gorotines
in such a way that there are never any cycles.

Not having any cycles has 3 benifits: 1) It makes deadlocks impossible, even
without buffering on go channels, which are unnecceary. 2) It minimizes the
amount goroutines have data state that is out of sync, since data can flow in
only one direction. And it makes what discrepancies that remain easier to reason
about for the same reason, data can flow in only one direction. 3) It makes the
shutdown procedure straightforward. Goroutines shut themselves down in the order
of the arrows on the directed graph.

If cycles must be in the design, try to minimize them. Whatever cycles are left,
you will have to think very hard about the exact timing of the execution of your
goroutines in them.

Once you've implemented your design, you'll have to think
twice before adding go channels that weren't there originally. You might think,
adding a go channel from goroutine B back to goroutine A, after you've already
got a go channel from goroutine A to goroutine B, would make things simpler --
two-way communication rather than one-way. If it turns out to be necessary, then
it's necessary, but because of the various problems with cycles listed above,
you should definitely think twice about doing that.

I've been wondering how the original Telnet chat systems and MUDs were
developed. Back then, most machines were single-processor (single core), so
maybe it was done just by funneling everything into a single process? Or maybe
they actually dealt with the complexities of mutexes and critical sections and
made everything run multithreaded?

I also learned some stuff about file descriptor limits in Linux (and Mac). If
you want to run a server that can have a lot of open TCP connections all at
once, you'll need to change the file descriptor limit.

I also realized I have things to learn: especially, how to properly test
concurrent code. I really need something analogous to a "unit test" for a
goroutine, but I haven't figured out a way to do that. I need to either figure
out a way to do it or do some research and learn systems other people have
figured out. The test program I came up with served to do a basic "stress test"
of the system, but feels inadaquate to really guarantee the system is rock
solid.



## BUILD INSTRUCTIONS

These instructions are for Linux and Mac machines. If you're on a Windows
machine, you're going to need Cygwin or MinGW to get SQLite3 working. (The
SQLite3 driver for Go requires gcc, and Windows doesn't have it by default.)
This is enough of a pain to get working that you might consider just ripping out
SQLite3 and using a different database system. The code is written to use the
standard "sql" package in Go, so it should be straightforward to switch to any
other database that supports that interface. (Or just use a Mac or Linux
machine.)

### Components you need to get:

```
$ go get github.com/mattn/go-sqlite3

$ go get golang.org/x/crypto/bcrypt

$ go get github.com/reiver/go-oi
```

You do not need to go get github.com/reiver/go-telnet because you'll be using
the modified version from this zip file.

move go-telnet-mod to your go/src directory (usually ~/go/src)

go to the go-telnet-mod directory and install it with

```
$ go install
```

At this point, you should be able to run the server (daemon) by going to the
daemon directory and running:

```
$ go run *go
```

If you want to make a compiled executable, use this command:

```
$ go build wteld.go datastructures.go helper.go channelmaster.go chatchannel.go doppelganger.go servetelnet.go heartbeat.go
```

This will give you an executable called wteld.



On Mac, I get a linker warning (not error) during the build, but it doesn't seem
to prevent the program from running:

```
ld: warning: building for macOS, but linking in object file (/var/folders/45/b384hxf51kncw9t31j3vgybr0000gn/T/go-link-680455127/go.o) built for 
```

I think this warning has something to do with how the SQLite3 driver evokes gcc
under the hood. But as noted, the system seems to work anyway.



### How To Change The Number Of File Descriptors On A Linux System

The standard way to change the number of file descriptors on a Linux system is
to edit /etc/sysctl.conf with

```
    fs.file-max = 8388608
```

(I'm using 8,388,608 as "large enough number"),

add 

```
    wayne hard nofile 8388608
```

(assuming I'm logged in as "wayne" -- substitute your username here)

to `/etc/security/limits.conf`

and add

```
    session required pam_limits.so
```

to `/etc/pam.d/common-session and /etc/pam.d/common-session-noninteractive` .

In my case this didn't turn out to be enough, because I was using Linux Mint
with a GUI process that was also imposing a limit. To fix that I added

```
    DefaultLimitNOFILE=8388608
```

to `/etc/systemd/system.conf` . (Don't include the indentation when adding the
lines. The indentation is just to make the exposition here clear.)









## EXTERNAL RESOURCES USED ON THIS PROJECT

### Packages from GitHub:

- github.com/mattn/go-sqlite3
- golang.org/x/crypto/bcrypt
- github.com/reiver/go-oi

### The reason bcrypt chosen for password hashing:

- https://blog.ircmaxell.com/2014/03/why-i-dont-recommend-scrypt.html

### bcrypt from:

- https://godoc.org/golang.org/x/crypto/bcrypt

### Ideas followed for getting test client working:

- https://stackoverflow.com/questions/49226145/telnet-client-in-go

### Telnet protocol links:
- http://mars.netanya.ac.il/~unesco/cdrom/booklet/HTML/NETWORKING/node300.html
- http://pcmicro.com/netfoss/telnet.html
- http://pcmicro.com/netfoss/telnet2.html

### IRC commands, used for ideas for the commands used in the program:
- https://www.mirc.com/help/html/index.html?basic_irc_commands.html

### Instructions for increasing file descriptor limit:
- https://www.tecmint.com/increase-set-open-file-limits-in-linux/
- https://superuser.com/questions/1200539/cannot-increase-open-file-limit-past-4096-ubuntu



### Changes Made To go-telnet:


If you're curious what exactly I changed in the go-telnet package, if you get
the original go-telnet (which you don't have to do to build the program) from
https://github.com/reiver/go-telnet , if you go to 

```
go-telnet/data_writer.go
```

and find the write64 function, you'll see an "if" block, "if IAC == datum". I
just took that "if" block out and left the buffer.WriteByte in its place. That's
it.

If I may, a few additional comments on go-telnet. It wraps a TCP connection
(net.Conn) in a buffer (go-telnet/server.go line 176 and go-telnet/data_reader.go
line 66). Presumably the TCP/IP stack already has a buffer (possibly several) so
this seems redundant. I suspect there is unnecessary buffering going on. I
considered not using go-telnet at all, and writing the code directly against the
Go "net" package. I've done this before and never run into the "io.ErrShortWrite"
issue that, according to the comments, is the justification for creating the
oi.LongWrite function. It could be that I never saw this because I was writing
directly to the "net" package, and ErrShortWrite seems to be part of the "io"
package (see https://golang.org/pkg/io/ ), so unless you're going through the
"io" package, you might never see it. In the end, though, I stuck with go-telnet
and go-oi because 1) after the initial changes to deal wit the "interpret as
command" (IAC) issue, everything worked -- including no sign of performance or
scalability problems -- and 2) the fact that it supported TELNETS, the secure,
SSL-enabled version of the Telnet protocol, meant that if there was ever a need
to use TELNETS, it should be simple to switch the program over.

