.PHONY: build
build:
	GOOS=linux GOARCH=amd64 go build -o bootstrap
	zip function.zip bootstrap
	rm bootstrap

.PHONY: test
test:
	go test
