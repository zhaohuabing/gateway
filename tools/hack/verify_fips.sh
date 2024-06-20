#!/bin/bash

binary=$1

echo "Checking whether compiled binaries have BoringSSL enabled ..."
echo "* checking ${binary} ..."

echo "  * checking 'go version' ..."
if ! go version "${binary}" | grep 'X:boringcrypto' ; then
    echo "  ! 'go version <binary>' returned value without 'X:boringcrypto': $(go version "${binary}")"
    exit 2
fi

echo "  * checking 'strings' ..."
if ! strings "${binary}" | grep --quiet '_Cfunc__goboringcrypto_' ; then
    echo "  ! 'strings <binary>' did not return expected BoringSSL symbol names"
    exit 2
fi

echo "  + BoringSSL is enabled in ${binary}"
