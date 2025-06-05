.PHONY: build
build:
	rm -f function.zip
	GOOS=linux GOARCH=amd64 go build -o bootstrap
	zip function.zip bootstrap
	rm bootstrap

.PHONY: test
test:
	go test
