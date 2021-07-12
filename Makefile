VERSION ?= $(shell git describe --tags)
CGO_ENABLED ?= 0
export LDFLAGS += -X github.com/epinio/epinio/internal/version.Version=$(VERSION)

########################################################################
## Development

build: embed_files build-amd64

build-all: embed_files build-amd64 build-arm64 build-arm32 build-windows build-darwin

build-all-small:
	@$(MAKE) LDFLAGS+="-s -w" build-all

build-arm32:
	GOARCH="arm" GOOS="linux" CGO_ENABLED=$(CGO_ENABLED) go build $(BUILD_ARGS) -ldflags '$(LDFLAGS)' -o dist/epinio-linux-arm32

build-arm64:
	GOARCH="arm64" GOOS="linux" CGO_ENABLED=$(CGO_ENABLED) go build $(BUILD_ARGS) -ldflags '$(LDFLAGS)' -o dist/epinio-linux-arm64

build-amd64:
	GOARCH="amd64" GOOS="linux" CGO_ENABLED=$(CGO_ENABLED) go build $(BUILD_ARGS) -ldflags '$(LDFLAGS)' -o dist/epinio-linux-amd64

build-windows:
	GOARCH="amd64" GOOS="windows" CGO_ENABLED=$(CGO_ENABLED) go build $(BUILD_ARGS) -ldflags '$(LDFLAGS)' -o dist/epinio-windows-amd64

build-darwin:
	GOARCH="amd64" GOOS="darwin" CGO_ENABLED=$(CGO_ENABLED) go build $(BUILD_ARGS) -ldflags '$(LDFLAGS)' -o dist/epinio-darwin-amd64

build-s390x:
	GOARCH="s390x" GOOS="linux" CGO_ENABLED=$(CGO_ENABLED) go build $(BUILD_ARGS) -ldflags '$(LDFLAGS)' -o dist/epinio-linux-s390x

build-images:
	@./scripts/build-images.sh

compress:
	upx --brute -1 ./dist/epinio-linux-arm32
	upx --brute -1 ./dist/epinio-linux-arm64
	upx --brute -1 ./dist/epinio-linux-amd64
	upx --brute -1 ./dist/epinio-windows-amd64
	upx --brute -1 ./dist/epinio-darwin-amd64

test: embed_files
	ginkgo -r -p -race -failOnPending helpers internal

# acceptance is not part of the unit tests, and has its own target, see below.

GINKGO_NODES ?= 2
FLAKE_ATTEMPTS ?= 2

acceptance-cluster-delete:
	k3d cluster delete epinio-acceptance

acceptance-cluster-setup:
	@./scripts/acceptance-cluster-setup.sh

test-acceptance: showfocus embed_files
	ginkgo -nodes ${GINKGO_NODES} -stream -randomizeAllSpecs --flakeAttempts=${FLAKE_ATTEMPTS} -failOnPending acceptance/. acceptance/api/v1/.


test-acceptance-api: showfocus embed_files
	ginkgo -nodes ${GINKGO_NODES} -stream -randomizeAllSpecs --flakeAttempts=${FLAKE_ATTEMPTS} -failOnPending acceptance/api/v1/.

test-acceptance-cli: showfocus embed_files
	ginkgo -nodes ${GINKGO_NODES} -stream -randomizeAllSpecs --flakeAttempts=${FLAKE_ATTEMPTS} -failOnPending acceptance/.

showfocus:
	@if test `cat acceptance/*.go | grep -c 'FIt\|FWhen\|FDescribe\|FContext'` -gt 0 ; then echo ; echo 'Focus:' ; grep 'FIt\|FWhen\|FDescribe\|FContext' acceptance/*.go ; echo ; fi

generate:
	go generate ./...

lint: embed_files
	go vet ./...

tidy:
	go mod tidy

fmt:
	go fmt ./...

patch-epinio-deployment:
	@./scripts/patch-epinio-deployment.sh

getstatik:
	( [ -x "$$(command -v statik)" ] || go get github.com/rakyll/statik@v0.1.7 )

wrap_registry_chart:
	helm package ./assets/container-registry/chart/container-registry/ -d assets/embedded-files

update_google_service_broker:
	@./scripts/update-google-service-broker.sh

update_tekton:
	mkdir -p assets/embedded-files/tekton
	wget https://storage.googleapis.com/tekton-releases/pipeline/previous/v0.23.0/release.yaml -O assets/embedded-files/tekton/pipeline-v0.23.0.yaml
	wget https://storage.googleapis.com/tekton-releases/triggers/previous/v0.12.1/release.yaml -O assets/embedded-files/tekton/triggers-v0.12.1.yaml
	wget https://github.com/tektoncd/dashboard/releases/download/v0.15.0/tekton-dashboard-release.yaml -O assets/embedded-files/tekton/dashboard-v0.15.0.yaml

update_qsecrets:
	mkdir -p assets/embedded-files/
	wget https://cloudfoundry-incubator.github.io/quarks-helm/quarks-secret-1.0.760.tgz -O assets/embedded-files/quarks-secret-1.0.760.tgz

embed_files: getstatik wrap_registry_chart
	statik -m -f -src=./assets/embedded-files -dest assets
	statik -m -f -src=./assets/embedded-web-files/views -ns webViews -p statikWebViews -dest assets
	statik -m -f -src=./assets/embedded-web-files/assets -ns webAssets -p statikWebAssets -dest assets

help:
	( echo _ _ ___ _____ ________ Overview ; epinio help ; for cmd in apps completion create-org delete help info install orgs push target uninstall ; do echo ; echo _ _ ___ _____ ________ Command $$cmd ; epinio $$cmd --help ; done ; echo ) | tee HELP


########################################################################
# Docs

swagger:
	swagger generate spec > swagger.json

swagger-serve:
	swagger serve swagger.json

########################################################################
# Support

tools-install:
	@./scripts/tools-install.sh

tools-versions:
	@./scripts/tools-versions.sh

########################################################################
# Kube dev environments

minikube-start:
	@./scripts/minikube-start.sh

minikube-delete:
	@./scripts/minikube-delete.sh
