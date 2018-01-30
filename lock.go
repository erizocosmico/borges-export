package export

import (
	"sync"

	"github.com/sirupsen/logrus"
)

type locker struct {
	mut   sync.Mutex
	locks map[string]*mutex
}

func newLocker() *locker {
	return &locker{
		locks: make(map[string]*mutex),
	}
}

func (l *locker) lock(id string) *mutex {
	var lock *mutex

	l.mut.Lock()
	defer l.mut.Unlock()
	if lo, ok := l.locks[id]; !ok {
		lock = &mutex{id, new(sync.Mutex)}
		l.locks[id] = lock
	} else {
		lock = lo
	}

	return lock
}

type mutex struct {
	id string
	*sync.Mutex
}

func (l *mutex) Lock() {
	logrus.WithField("id", l.id).Debug("lock")
	l.Mutex.Lock()
}

func (l *mutex) Unlock() {
	logrus.WithField("id", l.id).Debug("unlock")
	l.Mutex.Unlock()
}
