FROM debian:stable-slim
WORKDIR /app
RUN apt-get update && apt-get -y upgrade && apt-get install -y --no-install-recommends \
  libssl-dev \
  ca-certificates \
  && apt-get clean \
  && rm -rf /var/lib/apt/lists/*
COPY dugtrio-proxy-* /app/dugtrio-proxy
EXPOSE 8080
ENTRYPOINT ["./dugtrio-proxy"]
CMD []
