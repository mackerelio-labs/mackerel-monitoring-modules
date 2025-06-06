.PHONY: build
build:
	$(MAKE) -C lambda build

.PHONY: test
test:
	$(MAKE) -C lambda test

.PHONY: bump-version
bump-version:
	$(MAKE) -C lambda bump-version
	perl -i -pe 's/\?ref=v[\d\.]+/\?ref=v$(VERSION)/g' README.md
