package main

import (
	"log"
	"strconv"
	"strings"
	"time"
)

//
// This function can be turned into a call to panic for debugging purposes, but
// log errors in production.
//
func logError(msg string) {
	// panic(msg)
	log.Println(msg)
}

//
// Handy trimmer we use everywhere, gets rid of white space at beginnings and
// ends of strings.
//
func trim(strn string) string {
	return strings.Trim(strn, " \t\n\r")
}

//
// Some more handy helper functions.
//

func intToStr(ii int) string {
	return strconv.FormatInt(int64(ii), 10)
}

func int64ToStr(ii int64) string {
	return strconv.FormatInt(ii, 10)
}

func timeNow() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func deslash(strn string) string {
	return strings.Replace(strn, "/", "", -1)
}
