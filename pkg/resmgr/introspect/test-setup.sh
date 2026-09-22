#!/bin/sh

# Our collector tests run against the sysfs fixtures of pkg/sysfs.
# They are shared rather than duplicated; keep this in sync if those
# tarballs are ever renamed.

set -e

mkdir -p testdata
tar -C testdata -xJf ../../sysfs/test-data-sample1.tar.xz
tar -C testdata -xJf ../../sysfs/test-data-sample2.tar.xz
