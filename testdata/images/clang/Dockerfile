# Image for the clang end-to-end test: clang, its sanitizer runtimes and
# llvm-symbolizer (clang's ASan needs it for function names)
# all from Debian's own packages. Build it with:
#
#   docker build -t reprobot-test-clang testdata/images/clang
FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends clang libclang-rt-dev llvm make libc6-dev \
 && rm -rf /var/lib/apt/lists/*
