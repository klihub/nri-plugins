#!/bin/bash
set -e

owner="fmuyassarov"
repo="nri-plugins"
index_file="index.yaml"

# Fetch browser download URLs for releases
releases=$(curl -s "https://api.github.com/repos/$owner/$repo/releases" | jq -r '.[].assets[].browser_download_url')

# Check if index.yaml exists, if not create it
if [ ! -f "$index_file" ]; then
    touch "$index_file"
fi

# Check if releases were found
if [ -n "$releases" ]; then
    # Loop through the URLs
    for url in $releases; do
        # Check if the URL path exists in index.yaml
        # and if not, update the index.yaml accordingly
        if ! grep -q "$url" "$index_file"; then
            wget "$url"
            base_url=$(dirname "$url")
            if ! helm repo index . --url "$base_url" --merge "$index_file"; then
                echo "Failed to update "$index_file" for: $base_url"
            fi
        fi
    done
else
    echo "No releases found. Exiting..."
fi
