#!/usr/bin/env bash
[ -z "$DEBUG" ] || set -x
set -eo pipefail

# Builds the standalone schemagen CLI
# (can it be added to the single binary instead?)
# Usage, from repo root: ./contrib/schemagen-cli/build.sh

DIST=contrib/schemagen-cli/dist
[ -d $DIST ] && rm -r $DIST
mkdir $DIST

package=./contrib/schemagen-cli
output_base=$DIST/dgraph-schemagen
platforms=("linux/amd64" "darwin/amd64")

for platform in "${platforms[@]}"; do
    platform_split=(${platform//\// })
    GOOS=${platform_split[0]}
    GOARCH=${platform_split[1]}
    output=$output_base'-'$GOOS'-'$GOARCH
    [ $GOOS != "windows" ] || output_name+='.exe'
    env GOOS=$GOOS GOARCH=$GOARCH go build -o $output $package
done

(cd contrib/schemagen-cli/dist/; shasum -a 256 * | tee sha256.txt)
