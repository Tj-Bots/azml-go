FROM golang:bookworm AS builder

WORKDIR /usr/src/app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /usr/local/bin/azml-go .

FROM aquib4040/azmlgo:latest

ENV GODEBUG="madvdontneed=1"
ENV TZ="Asia/Kolkata"
ENV DEBIAN_FRONTEND="noninteractive"

WORKDIR /usr/src/app

# Install git, speedtest-cli, curl, ca-certificates, and tzdata without interactive debconf prompts
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends git speedtest-cli curl ca-certificates tzdata unzip && \
    ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone && \
    rm -rf /var/lib/apt/lists/*

# Copy the exact Go compiler from builder stage (no re-downloading required)
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

# Pre-cache Go modules and the compiled packages from builder stage: the update on every start
# (and /restart) compiles only what changed, in seconds and with little memory
COPY --from=builder /go/pkg/mod /go/pkg/mod
COPY --from=builder /root/.cache/go-build /root/.cache/go-build
ENV GOPATH="/go"
# same settings as the builder stage, or the cache does not match and everything is compiled again
ENV CGO_ENABLED=0
ENV GOFLAGS="-p=1"

# TJ names: aria2c of the base image (named studymaterial / dlurl there) runs as tjurl,
# ffmpeg as tjff and ffprobe as tjprobe
RUN B="$(command -v tjurl || command -v dlurl || command -v studymaterial || command -v aria2c)" && \
    if [ "$B" != "/usr/local/bin/tjurl" ]; then mv "$B" /usr/local/bin/tjurl; fi && \
    chmod a+rx /usr/local/bin/tjurl && \
    tjurl --version >/dev/null && \
    F="$(command -v tjff || command -v ffmpeg)" && \
    if [ "$F" != "/usr/local/bin/tjff" ]; then mv "$F" /usr/local/bin/tjff; fi && \
    P="$(command -v tjprobe || command -v ffprobe)" && \
    if [ "$P" != "/usr/local/bin/tjprobe" ]; then mv "$P" /usr/local/bin/tjprobe; fi && \
    chmod a+rx /usr/local/bin/tjff /usr/local/bin/tjprobe && \
    tjff -version >/dev/null && tjprobe -version >/dev/null && \
    mkdir -p /usr/local/lib/tjdl && \
    printf '#!/bin/sh\nexec /usr/local/bin/tjff "$@"\n' > /usr/local/lib/tjdl/ffmpeg && \
    printf '#!/bin/sh\nexec /usr/local/bin/tjprobe "$@"\n' > /usr/local/lib/tjdl/ffprobe && \
    chmod a+rx /usr/local/lib/tjdl/ffmpeg /usr/local/lib/tjdl/ffprobe

RUN mkdir -p /usr/src/app/downloads /usr/src/app/Thumbnails

# Latest yt-dlp (YouTube changes often), installed as tjdl, + Deno, which it needs for YouTube
RUN rm -f /usr/local/bin/yt-dlp && \
    curl -fsSL https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux -o /usr/local/bin/tjdl && \
    chmod a+rx /usr/local/bin/tjdl && \
    curl -fsSL https://github.com/denoland/deno/releases/latest/download/deno-x86_64-unknown-linux-gnu.zip -o /tmp/deno.zip && \
    unzip -o /tmp/deno.zip -d /usr/local/bin && \
    chmod a+rx /usr/local/bin/deno && rm -f /tmp/deno.zip && \
    (tjdl --version && deno --version || true)

# rclone for TJ RClone (/mirror -up rc), installed as tjrc
RUN curl -fsSL https://downloads.rclone.org/rclone-current-linux-amd64.zip -o /tmp/rc.zip && \
    unzip -q /tmp/rc.zip -d /tmp/rc && \
    mv /tmp/rc/rclone-*-linux-amd64/rclone /usr/local/bin/tjrc && \
    chmod a+rx /usr/local/bin/tjrc && rm -rf /tmp/rc.zip /tmp/rc && \
    tjrc version >/dev/null

# Install HatchZip for in-place, space-saving archive extraction
RUN (curl -fsSL https://raw.githubusercontent.com/xevrion/hatchzip/main/install.sh | HATCHZIP_INSTALL_DIR=/usr/local/bin sh) \
    || ( \
        ARCH=$(uname -m) && \
        case "$ARCH" in \
            x86_64|amd64) HZ_ARCH="x86_64" ;; \
            aarch64|arm64) HZ_ARCH="aarch64" ;; \
            *) HZ_ARCH="x86_64" ;; \
        esac && \
        curl -fsSL "https://github.com/xevrion/hatchzip/releases/latest/download/hatchzip-${HZ_ARCH}-linux.tar.gz" -o /tmp/hatchzip.tar.gz && \
        tar -xzf /tmp/hatchzip.tar.gz -C /tmp && \
        cp "/tmp/hatchzip-${HZ_ARCH}-linux/hatchzip" /usr/local/bin/hatchzip && \
        chmod +x /usr/local/bin/hatchzip && \
        ln -sf /usr/local/bin/hatchzip /usr/local/bin/hz && \
        rm -rf /tmp/hatchzip* \
    ) && (hatchzip --version || true)

# Copy source repository to /usr/src/app
COPY . /usr/src/app
RUN chmod -R 777 /usr/src/app

COPY --from=builder /usr/local/bin/azml-go /usr/local/bin/azml-go
RUN chmod a+rx /usr/local/bin/azml-go

EXPOSE 23434

CMD ["azml-go"]
