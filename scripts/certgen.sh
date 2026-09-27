#!/bin/sh
set -eu
umask 077
out=${1:?output directory}; name=${2:?identity name}
mkdir -p "$out"
test ! -e "$out/$name.key"
openssl ecparam -name prime256v1 -genkey -noout -out "$out/$name.key"
openssl req -new -x509 -sha256 -days 365 -key "$out/$name.key" -subj "/CN=$name" -out "$out/$name.crt"
openssl x509 -in "$out/$name.crt" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256 | awk '{print $NF}' > "$out/$name.pin"
