# Builds a Linux release of the Christmas duck server and packages it for deployment.
#
#   make build                 dist/ with the server and deploy files, zipped flat as dist/christmas-duck.zip
#   make build GOARCH=arm64    for an ARM server
#   make deploy                build, copy the zip to $(DEPLOY_HOST):$(DEPLOY_DIR) and run deploy.sh there
#   make test | clean

APP     := christmas-duck
GOOS    ?= linux
GOARCH  ?= amd64
DIST    := dist
ASSETS  := deploy/deploy.sh deploy/christmas-duck.env.example

DEPLOY_HOST ?= bryan@oyd
DEPLOY_DIR  ?= /home/bryan/christmas-duck

.PHONY: build deploy test clean

build: test
	rm -rf $(DIST)
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags="-s -w" -o $(DIST)/$(APP) .
	cp $(ASSETS) $(DIST)/
	cd $(DIST) && zip -q $(APP).zip $(APP) $(notdir $(ASSETS))
	@echo "Built $(DIST)/$(APP).zip"

deploy: build
	ssh $(DEPLOY_HOST) 'mkdir -p $(DEPLOY_DIR)'
	scp $(DIST)/$(APP).zip $(DEPLOY_HOST):$(DEPLOY_DIR)/
	ssh $(DEPLOY_HOST) 'cd $(DEPLOY_DIR) && unzip -oq $(APP).zip && ./deploy.sh'

test:
	go test ./...

clean:
	rm -rf $(DIST)
