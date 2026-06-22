FROM --platform=$BUILDPLATFORM golang:1.25 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X main.version=${VERSION}" -o /tplr ./cmd/tplr

FROM alpine:3.20
COPY --from=build /tplr /usr/local/bin/tplr
ENTRYPOINT ["tplr"]
