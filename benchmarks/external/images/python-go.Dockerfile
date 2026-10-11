FROM golang:1.23.12-bookworm@sha256:167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db AS go
FROM python:3.12.10-slim-bookworm@sha256:fd95fa221297a88e1cf49c55ec1828edd7c5a428187e67b5d1805692d11588db
COPY --from=go /usr/local/go /usr/local/go
RUN apt-get update && apt-get install -y --no-install-recommends git time ca-certificates && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir pytest==8.4.2 iniconfig==2.3.1 packaging==26.3 pluggy==1.6.0 pygments==2.21.0
ENV PATH=/usr/local/go/bin:$PATH PYTHONPATH=src GOTOOLCHAIN=local GOCACHE=/tmp/go-cache GOPATH=/tmp/go-path GOPROXY=off GOSUMDB=off
