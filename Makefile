install_dep:
	go get github.com/golang/dep/cmd/dep

install: install_dep
	dep ensure

build: install
	go build -tags norwfs export.go