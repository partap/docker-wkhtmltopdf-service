#!/usr/bin/env bash

# Build the image
docker buildx build --platform linux/amd64 -t inventivetec/wkhtmltopdf:latest --push .
