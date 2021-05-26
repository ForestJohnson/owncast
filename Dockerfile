FROM golang:1.16.4-buster AS build
RUN apt-get update && apt-get install -y crossbuild-essential-armhf

WORKDIR /build
COPY . /build
RUN CGO_ENABLED=1 CC=arm-linux-gnueabihf-gcc GOARCH=arm GOOS=linux go build -tags osusergo,netgo,sqlite_omit_load_extension -installsuffix cgo -ldflags '-extldflags "-static"' -o owncast .


FROM arm32v7/alpine
RUN apk add --no-cache ffmpeg ffmpeg-libs

WORKDIR /app
COPY webroot /app/webroot
COPY static /app/static
COPY nsswitch.conf /etc/nsswitch.conf
COPY --from=build /build/owncast /app/owncast

EXPOSE 8080 1935
CMD ["/app/owncast"]
