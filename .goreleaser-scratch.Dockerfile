FROM gcr.io/distroless/static-debian11:latest
WORKDIR /app
COPY dugtrio-proxy-* /app/dugtrio-proxy
EXPOSE 8080
ENTRYPOINT ["./dugtrio-proxy"]
CMD []
