package export

import (
	"bytes"
	"encoding/csv"
	"io/ioutil"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/src-d/core-retrieval.v0/model"
	"gopkg.in/src-d/core-retrieval.v0/repository"
)

// Export to the given output csv file all the processed repositories in
// the given store.
func Export(
	store *model.RepositoryStore,
	txer repository.RootedTransactioner,
	outputFile string,
) {
	rs, err := store.Find(model.NewRepositoryQuery().
		FindByStatus(model.Fetched))
	if err != nil {
		logrus.Fatal(err)
	}

	repos, processed, failed := processRepos(txer, rs)
	setForks(repos)
	writeResult(outputFile, repos)

	logrus.WithFields(logrus.Fields{
		"processed": processed,
		"failed":    failed,
		"total":     failed + processed,
	}).Info("finished processing all repositories")
}

func writeResult(file string, repos []*repositoryData) {
	logrus.Debug("start writing result")
	start := time.Now()
	defer func() {
		logrus.WithField("elapsed", time.Since(start)).Debug("finished writing result")
	}()

	if _, err := os.Stat(file); err != nil && !os.IsNotExist(err) {
		logrus.WithField("err", err).WithField("file", file).
			Fatal("unexpected error reading file")
	} else if err == nil {
		logrus.WithField("file", file).Warn("file exists, it will be deleted")
		if err := os.Remove(file); err != nil {
			logrus.WithField("err", err).WithField("file", file).
				Fatal("unable to remove file")
		}
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(csvHeader); err != nil {
		logrus.WithField("err", err).Fatal("unable to write csv header")
	}

	for _, data := range repos {
		if err := w.Write(data.toRecord()); err != nil {
			logrus.WithField("err", err).Fatal("unable to write csv record")
		}
	}

	w.Flush()
	if err := ioutil.WriteFile(file, buf.Bytes(), 0644); err != nil {
		logrus.WithField("err", err).WithField("file", file).Fatal("unable to write to file")
	}
}
