package main

import (
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hhatto/gocloc"
	"github.com/sirupsen/logrus"
	"github.com/vmarkovtsev/go-license"
	"gopkg.in/src-d/core-retrieval.v0"
	"gopkg.in/src-d/core-retrieval.v0/model"
	"gopkg.in/src-d/core-retrieval.v0/repository"
	"gopkg.in/src-d/enry.v1"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
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

	store := core.ModelRepositoryStore()
	rs, err := store.Find(model.NewRepositoryQuery().
		FindByStatus(model.Fetched))
	if err != nil {
		logrus.Fatal(err)
	}

	repos, processed, failed := processRepos(core.RootedTransactioner(), rs)
	setForks(repos)
	writeResult(*output, repos)

	logrus.WithFields(logrus.Fields{
		"processed": processed,
		"failed":    failed,
		"total":     failed + processed,
	}).Info("finished processing all repositories")
}

func processRepos(
	txer repository.RootedTransactioner,
	rs *model.RepositoryResultSet,
) (repos []*Repository, processed int, failed int) {
	logrus.Debug("start processing repos")
	start := time.Now()
	defer func() {
		logrus.WithField("elapsed", time.Since(start)).Debug("finished processing repos")
	}()

	for rs.Next() {
		failed++
		repo, err := rs.Get()
		if err != nil {
			logrus.WithField("err", err).Error("unable to get next repository")
			continue
		}

		log := logrus.WithField("repo", repo.ID)
		data, err := process(repo, txer)
		if err != nil {
			log.WithField("err", err).Error("unable to process repository")
			continue
		}

		repos = append(repos, data)
		processed++
		failed--
	}

	return
}

func writeResult(file string, repos []*Repository) {
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

func repoData(repo *git.Repository, dbRepo *model.Repository) (*Repository, error) {
	log := logrus.WithField("repo", dbRepo.ID)
	log.Debug("start building repo data")
	start := time.Now()
	defer func() {
		log.WithField("elapsed", time.Since(start)).Debug("finished building repo data")
	}()

	var data Repository

	// default value
	data.URL = dbRepo.Endpoints[0]
	// initialize to first github url, if any
	for _, e := range dbRepo.Endpoints {
		if strings.Contains(e, "github.com") {
			data.URL = e
			break
		}
	}

	head, err := getHEAD(repo, dbRepo)
	if err != nil {
		return nil, fmt.Errorf("unable to get HEAD ref: %s", err)
	}

	files, err := headFiles(repo, head)
	if err != nil {
		return nil, fmt.Errorf("unable to get head files: %s", err)
	}
	data.Files = len(files)

	langs, err := languageReport(files)
	if err != nil {
		return nil, fmt.Errorf("unable to get lang report: %s", err)
	}

	path, err := writeToTempDir(files)
	if err != nil {
		return nil, fmt.Errorf("unable to write files to temp dir: %s", err)
	}

	defer func() {
		if err := os.RemoveAll(path); err != nil {
			logrus.WithField("dir", path).Error("unable to remove temp dir")
		}
	}()

	lines, err := lineCounts(path, files)
	if err != nil {
		return nil, err
	}

	data.Languages = mergeLanguageData(langs, lines)

	data.HEADCommits, err = headCommits(repo, head)
	if err != nil {
		return nil, fmt.Errorf("unable to get head commits: %s", err)
	}

	data.License, err = repositoryLicense(files)
	if err != nil {
		return nil, fmt.Errorf("unable to get license: %s", err)
	}

	return &data, nil
}

type Repository struct {
	URL         string
	SivaFiles   []string
	Files       int
	Languages   map[string]Language
	HEADCommits int64
	Commits     int64
	Branches    int
	Forks       int
	License     string
}

func (r Repository) toRecord() []string {
	var (
		langs            []string
		langBytes        = make([]string, len(r.Languages))
		langLines        = make([]string, len(r.Languages))
		langFiles        = make([]string, len(r.Languages))
		langEmptyLines   = make([]string, len(r.Languages))
		langCodeLines    = make([]string, len(r.Languages))
		langCommentLines = make([]string, len(r.Languages))
	)

	for lang := range r.Languages {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	for i, lang := range langs {
		l := r.Languages[lang]
		langBytes[i] = fmt.Sprint(l.Usage.Bytes)
		langFiles[i] = fmt.Sprint(l.Usage.Files)
		langLines[i] = fmt.Sprint(l.Usage.Lines)
		langEmptyLines[i] = fmt.Sprint(l.Lines.Blank)
		langCodeLines[i] = fmt.Sprint(l.Lines.Code)
		langCommentLines[i] = fmt.Sprint(l.Lines.Comments)
	}

	return []string{
		r.URL,                     // "URL"
		join(r.SivaFiles),         // "SIVA_FILENAMES"
		fmt.Sprint(r.Files),       // "FILE_COUNT"
		join(langs),               // "LANGS"
		join(langBytes),           // "LANGS_BYTE_COUNT"
		join(langLines),           // "LANGS_LINES_COUNT"
		join(langFiles),           // "LANGS_FILES_COUNT"
		fmt.Sprint(r.HEADCommits), // "COMMITS_COUNT"
		fmt.Sprint(r.Branches),    // "BRANCHES_COUNT"
		fmt.Sprint(r.Forks),       // "FORK_COUNT"
		join(langEmptyLines),      // "EMPTY_LINES_COUNT"
		join(langCodeLines),       // "CODE_LINES_COUNT"
		join(langCommentLines),    // "COMMENT_LINES_COUNT"
		r.License,                 // "LICENSE"
	}
}

func join(strs []string) string {
	return strings.Join(strs, ",")
}

var csvHeader = []string{
	"URL",
	"SIVA_FILENAMES",
	"FILE_COUNT",
	"LANGS",
	"LANGS_BYTE_COUNT",
	"LANGS_LINES_COUNT",
	"LANGS_FILES_COUNT",
	"COMMITS_COUNT",
	"BRANCHES_COUNT",
	"FORK_COUNT",
	"EMPTY_LINES_COUNT",
	"CODE_LINES_COUNT",
	"COMMENT_LINES_COUNT",
	"LICENSE",
}

func process(dbRepo *model.Repository, txer repository.RootedTransactioner) (*Repository, error) {
	log := logrus.WithField("repo", dbRepo.ID)
	log.Debug("start processing repository")
	start := time.Now()
	defer func() {
		log.WithField("elapsed", time.Since(start)).Debug("finished processing repository")
	}()

	var inits = make(map[model.SHA1]struct{})
	var empty model.SHA1
	var head model.SHA1
	for _, ref := range dbRepo.References {
		if ref.Name == "refs/heads/HEAD" {
			head = ref.Init
		}

		inits[ref.Init] = struct{}{}
	}

	if head == empty {
		return nil, fmt.Errorf("repository has no HEAD")
	}

	tx, err := txer.Begin(plumbing.NewHash(head.String()))
	if err != nil {
		return nil, fmt.Errorf("can't start transaction: %s", err)
	}

	repo, err := git.Open(tx.Storer(), nil)
	if err != nil {
		return nil, fmt.Errorf("can't open git repo: %s", err)
	}

	data, err := repoData(repo, dbRepo)
	if err != nil {
		return nil, fmt.Errorf("unable to get repo data: %s", err)
	}

	_ = tx.Rollback()

	for init := range inits {
		tx, err := txer.Begin(plumbing.NewHash(init.String()))
		if err != nil {
			return nil, fmt.Errorf("can't get root transaction: %s", err)
		}

		r, err := git.Open(tx.Storer(), nil)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("can't open root repo: %s", err)
		}

		iter, err := r.CommitObjects()
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("can't get root commits: %s", err)
		}

		n, err := countCommits(iter)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("can't count root commits: %s", err)
		}

		id, err := getRepoID(repo, dbRepo)
		if err != nil {
			return nil, err
		}

		refs, err := r.References()
		if err != nil {
			return nil, fmt.Errorf("can't get references: %s", err)
		}

		var refCount int
		err = refs.ForEach(func(ref *plumbing.Reference) error {
			if strings.HasSuffix(string(ref.Name()), "/"+id) {
				refCount++
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("unable to count refs: %s", err)
		}

		data.Branches += refCount
		data.Commits += n

		_ = tx.Rollback()
	}

	data.SivaFiles = sivaFiles(inits)

	return data, nil
}

func getHEAD(repo *git.Repository, repoDb *model.Repository) (*plumbing.Reference, error) {
	id, err := getRepoID(repo, repoDb)
	if err != nil {
		return nil, err
	}

	return repo.Reference(plumbing.ReferenceName("refs/heads/HEAD/"+id), true)
}

func getRepoID(repo *git.Repository, repoDb *model.Repository) (string, error) {
	cfg, err := repo.Config()
	if err != nil {
		return "", fmt.Errorf("unable to get config: %s", err)
	}

	var target string
Outer:
	for id, r := range cfg.Remotes {
		for _, u := range r.URLs {
			for _, e := range repoDb.Endpoints {
				if u == e {
					target = id
					break Outer
				}
			}
		}
	}

	if target == "" {
		return "", fmt.Errorf("unable to guess the repository from config for repo: %s", repoDb.ID)
	}

	return target, nil
}

func sivaFiles(inits map[model.SHA1]struct{}) []string {
	var files []string
	for init := range inits {
		files = append(files, fmt.Sprintf("%s.siva", init))
	}
	sort.Strings(files)
	return files
}

type Language struct {
	Lines LineCounts
	Usage LanguageUsage
}

func mergeLanguageData(
	usage map[string]LanguageUsage,
	counts map[string]LineCounts,
) map[string]Language {
	var result = make(map[string]Language)

	for lang, usage := range usage {
		count := counts[lang]
		result[lang] = Language{Lines: count, Usage: usage}
	}

	return result
}

type LineCounts struct {
	Blank    int64
	Code     int64
	Comments int64
}

func lineCounts(path string, files []*object.File) (map[string]LineCounts, error) {
	logrus.Debug("start building line counts")
	start := time.Now()
	defer func() {
		logrus.WithField("elapsed", time.Since(start)).Debug("finished building line counts")
	}()

	proc := gocloc.NewProcessor(gocloc.NewDefinedLanguages(), gocloc.NewClocOptions())

	var paths = make([]string, len(files))
	for i, f := range files {
		paths[i] = filepath.Join(path, f.Name)
	}

	result, err := proc.Analyze(paths)
	if err != nil {
		return nil, fmt.Errorf("can't analyze files: %s", err)
	}

	lineCounts := make(map[string]LineCounts)
	for lang, counts := range result.Languages {
		lineCounts[lang] = LineCounts{
			Blank:    int64(counts.Blanks),
			Code:     int64(counts.Code),
			Comments: int64(counts.Comments),
		}
	}

	return lineCounts, nil
}

func repositoryLicense(files []*object.File) (string, error) {
	for _, f := range files {
		if isLicenseFile(f) {
			content, err := f.Contents()
			if err != nil {
				return "", fmt.Errorf("can't get contents of file: %s", err)
			}
			lic := license.License{Text: content}
			if err := lic.GuessType(); err != nil && err != license.ErrUnrecognizedLicense {
				return "", fmt.Errorf("can't guess license type: %s", err)
			}

			return lic.Type, nil
		}
	}

	return "", nil
}

func isLicenseFile(file *object.File) bool {
	name := strings.ToLower(file.Name)
	for _, lf := range license.DefaultLicenseFiles {
		if lf == name {
			return true
		}
	}
	return false
}

func headCommits(repo *git.Repository, head *plumbing.Reference) (int64, error) {
	logrus.Debug("start counting HEAD commits")
	start := time.Now()
	defer func() {
		logrus.WithField("elapsed", time.Since(start)).Debug("finished counting HEAD commits")
	}()

	ci, err := repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return -1, fmt.Errorf("can't get HEAD log: %s", err)
	}

	return countCommits(ci)
}

func countCommits(iter object.CommitIter) (count int64, err error) {
	err = iter.ForEach(func(_ *object.Commit) error {
		count++
		return nil
	})
	return
}

func branches(repo *git.Repository) ([]string, error) {
	logrus.Debug("start counting branches")
	start := time.Now()
	defer func() {
		logrus.WithField("elapsed", time.Since(start)).Debug("finished counting branches")
	}()

	ri, err := repo.References()
	if err != nil {
		return nil, fmt.Errorf("can't get repo references: %s", err)
	}

	var refs []string
	err = ri.ForEach(func(ref *plumbing.Reference) error {
		if !ref.Name().IsTag() {
			refs = append(refs, ref.Name().String())
		}
		return nil
	})
	return refs, err
}

type LanguageUsage struct {
	Files int64
	Bytes int64
	Lines int64
}

func languageReport(files []*object.File) (map[string]LanguageUsage, error) {
	logrus.Debug("start building language report")
	start := time.Now()
	defer func() {
		logrus.WithField("elapsed", time.Since(start)).Debug("finished building language report")
	}()

	var idx = make(map[string]LanguageUsage)

	for _, f := range files {
		content, err := f.Contents()
		if err != nil {
			return nil, fmt.Errorf("can't get file contents: %s", err)
		}

		lang := enry.GetLanguage(f.Name, []byte(content))
		if lang == "" {
			continue
		}

		bytes := len(content)
		lines := len(strings.Split(content, "\n"))

		report := idx[lang]
		report.Files++
		report.Bytes += int64(bytes)
		report.Lines += int64(lines)
		idx[lang] = report
	}

	return idx, nil
}

func headFiles(repo *git.Repository, head *plumbing.Reference) ([]*object.File, error) {
	logrus.Debug("start getting files of HEAD")
	start := time.Now()
	defer func() {
		logrus.WithField("elapsed", time.Since(start)).Debug("finished getting files of HEAD")
	}()

	ci, err := repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, fmt.Errorf("can't get HEAD log: %s", err)
	}

	commit, err := ci.Next()
	if err != nil {
		return nil, fmt.Errorf("can't get first commit in HEAD: %s", err)
	}
	ci.Close()

	fi, err := commit.Files()
	if err != nil {
		return nil, fmt.Errorf("can't get commit files: %s", err)
	}

	var files []*object.File
	err = fi.ForEach(func(f *object.File) error {
		files = append(files, f)
		return nil
	})
	return files, err
}

func setForks(repos []*Repository) {
	logrus.Debug("setting forks for repositories")
	start := time.Now()
	var reposBySiva = make(map[string][]string)
	for _, r := range repos {
		for _, s := range r.SivaFiles {
			reposBySiva[s] = append(reposBySiva[s], r.URL)
		}
	}

	for _, r := range repos {
		for _, s := range r.SivaFiles {
			r.Forks += len(reposBySiva[s]) - 1 // don't take self into account
		}
	}
	logrus.WithField("elapsed", time.Since(start)).Debug("finished setting forks for repositories")
}

func writeToTempDir(files []*object.File) (base string, err error) {
	base, err = ioutil.TempDir(os.TempDir(), "borges-export")
	if err != nil {
		return "", fmt.Errorf("unable to create temp dir: %s", err)
	}

	defer func() {
		if err != nil {
			if removeErr := os.RemoveAll(base); removeErr != nil {
				logrus.Errorf("unable to remove temp dir after error (%s): %s", removeErr, err)
			}
		}
	}()

	for _, f := range files {
		path := filepath.Join(base, f.Name)
		if err = os.MkdirAll(filepath.Dir(path), 0744); err != nil {
			return "", err
		}

		var content string
		content, err = f.Contents()
		if err != nil {
			return "", err
		}

		err = ioutil.WriteFile(path, []byte(content), 0644)
		if err != nil {
			return "", err
		}
	}

	return base, nil
}
