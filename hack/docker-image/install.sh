#!/bin/bash
PROTOC_ZIP=protoc-3.7.1-linux-x86_64.zip
curl -OL https://github.com/google/protobuf/releases/download/v3.7.1/$PROTOC_ZIP
 
unzip -o $PROTOC_ZIP -d protoc3
mv protoc3/bin/* /usr/local/bin/
mv protoc3/include/* /usr/local/include/
rm -f $PROTOC_ZIP
rm -rf prtoc3
 
chmod -R +r /usr/local/include/
mkdir mkdir /.cache
chmod -R 777 /.cache
