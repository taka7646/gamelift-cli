REVISION := $(shell git rev-parse --short HEAD)
NAME := gamelift-cli
.PHONY:　build

build: bin/darwin/${NAME} bin/win/${NAME}.exe bin/linux/${NAME}

bin/darwin/${NAME}: *.go
	GOOS=darwin GOARCH=amd64 \
	go build -ldflags "-X main.hash=$(REVISION)" -o bin/darwin/${NAME} *.go

bin/win/${NAME}.exe: *.go
	GOOS=windows GOARCH=amd64 \
	go build -ldflags "-X main.hash=$(REVISION)" -o bin/win/${NAME}.exe *.go

bin/linux/${NAME}: *.go
	GOOS=linux GOARCH=amd64 \
	go build -ldflags "-X main.hash=$(REVISION)" -o bin/linux/${NAME} *.go

archive: bin/${NAME}.darwin.zip bin/${NAME}.win.zip bin/${NAME}.linux.zip

bin/${NAME}.darwin.zip: bin/darwin/${NAME}
	cd bin && \
	zip -j ${NAME}.darwin.zip darwin/${NAME}

bin/${NAME}.win.zip: bin/win/${NAME}.exe
	cd bin && \
	zip -j ${NAME}.win.zip win/${NAME}.exe

bin/${NAME}.linux.zip: bin/linux/${NAME}
	cd bin && \
	zip -j ${NAME}.linux.zip linux/${NAME}
