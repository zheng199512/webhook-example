#!/bin/bash
# 创建ca和cert
openssl genrsa -out config/certs/ca.key 2048
openssl req -x509 -new -nodes -key config/certs/ca.key     -subj "/CN=webhook-ca"    -sha256 -days 10000 -out config/certs/ca.crt
# 创建server
openssl genrsa -out config/certs/serve.key 2048
openssl req -new -sha256  -key config/certs/serve.key    -out config/certs/serve.csr  -config csr.cnf   -subj "/CN=webhook.zhenghongfei.svc"
#openssl x509  -noout -text -in config/certs/serve.csr
openssl x509 -req -days 365000 -sha256 -CA config/certs/ca.crt  -CAkey config/certs/ca.key -in  config/certs/serve.csr -out config/certs/serve.crt -CAcreateserial -extensions v3_req -extfile  csr.cnf