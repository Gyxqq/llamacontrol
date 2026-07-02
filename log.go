package main

import (
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

// log is the package-level logger.
// Default level is Info — debug/trace output is hidden in release builds.
// Set LLAMACONTROL_DEBUG=true (or 1) to enable verbose debug output.
var log = logrus.New()

func init() {
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006/01/02 15:04:05.000",
	})

	level := logrus.InfoLevel
	if v := strings.ToLower(os.Getenv("LLAMACONTROL_DEBUG")); v == "true" || v == "1" {
		level = logrus.DebugLevel
	}
	log.SetLevel(level)
}