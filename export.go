package export

import (
	"encoding/csv"
	"os"
	"os/signal"

	"github.com/sirupsen/logrus"
	"gopkg.in/src-d/core-retrieval.v0/model"
	"gopkg.in/src-d/core-retrieval.v0/repository"
	"gopkg.in/src-d/go-kallax.v1"
)

// Export to the given output csv file all the processed repositories in
// the given store.
func Export(
	store *model.RepositoryStore,
	txer repository.RootedTransactioner,
	outputFile string,
	limit uint64,
	offset uint64,
) {
	f, err := createOutputFile(outputFile)
	if err != nil {
		logrus.WithField("file", outputFile).WithField("err", err).
			Fatal("unable to create file")
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		logrus.WithField("err", err).Fatal("unable to write csv header")
	}
	w.Flush()

	rs, total, err := getResultSet(store, limit, offset)
	if err != nil {
		logrus.WithField("err", err).Fatal("unable to get result set")
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	repos := processRepos(txer, rs)
	var processed int
	for {
		select {
		case repo, ok := <-repos:
			if !ok {
				logrus.WithFields(logrus.Fields{
					"processed": processed,
					"failed":    total - int64(processed),
					"total":     total,
				}).Info("finished processing all repositories")
				return
			}

			logrus.WithField("repo", repo.URL).Debug("writing record to CSV")
			if err := w.Write(repo.toRecord()); err != nil {
				logrus.WithFields(logrus.Fields{
					"err":  err,
					"repo": repo.URL,
				}).Fatal("unable to write csv record")
			}
			w.Flush()
			processed++
		case <-signals:
			logrus.Warn("received an interrupt signal, stopping")
			return
		}
	}
}

func createOutputFile(outputFile string) (*os.File, error) {
	if _, err := os.Stat(outputFile); err != nil && !os.IsNotExist(err) {
		return nil, err
	} else if err == nil {
		logrus.WithField("file", outputFile).Warn("file exists, it will be deleted")
		if err := os.Remove(outputFile); err != nil {
			return nil, err
		}
	}

	return os.Create(outputFile)
}

func getResultSet(
	store *model.RepositoryStore,
	limit, offset uint64,
) (*model.RepositoryResultSet, int64, error) {
	query := model.NewRepositoryQuery().
		FindByStatus(model.Fetched)
	if limit > 0 {
		query = query.Limit(limit)
	}
	if limit > 0 {
		query = query.Offset(offset)
	}

	total, err := store.Count(query)
	if err != nil {
		return nil, 0, err
	}

	rs, err := store.Find(query.Order(kallax.Asc(model.Schema.Repository.ID)))
	if err != nil {
		return nil, 0, err
	}

	return rs, total, nil
}
