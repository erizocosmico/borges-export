package main

import (
	"flag"
	"os"

	"github.com/erizocosmico/borges-export"
	"github.com/sirupsen/logrus"
	"gopkg.in/src-d/core-retrieval.v0"
)

func main() {
	output := flag.String("o", "result.csv", "csv file path with the results")
	debug := flag.Bool("debug", false, "show debug logs")
	logfile := flag.String("logfile", "", "write logs to file")
	flag.Parse()

	if *debug {
		logrus.SetLevel(logrus.DebugLevel)
	} else {
		logrus.SetLevel(logrus.InfoLevel)
	}

	if *logfile != "" {
		_ = os.Remove(*logfile)
		f, err := os.Create(*logfile)
		if err != nil {
			logrus.WithField("err", err).Fatalf("unable to create log file: %s", *logfile)
		}

		defer func() {
			if err := f.Close(); err != nil {
				logrus.WithField("err", err).Error("unable to close log file")
			}
		}()

		logrus.SetOutput(f)
	}

	export.Export(
		core.ModelRepositoryStore(),
		core.RootedTransactioner(),
		*output,
	)
}
