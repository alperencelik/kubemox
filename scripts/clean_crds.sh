#!/bin/bash
set -euo pipefail

SRC_DIR=$1
DEST_DIR=$2

if [ -z "$SRC_DIR" ] || [ -z "$DEST_DIR" ]; then
  echo "Usage: $0 <source_directory> <destination_directory>"
  exit 1
fi

mkdir -p "$DEST_DIR"

echo "Cleaning CRDs from $SRC_DIR and copying to $DEST_DIR..."

for file in "$SRC_DIR"/*.yaml; do
  filename=$(basename "$file")
  # Remove the prefix from the filename
  new_filename=$(echo "$filename" | sed 's/^.*_//')
  dest_file="$DEST_DIR/$new_filename"
  
  echo "Processing $filename -> $new_filename..."

  # Use awk to filter out unwanted sections
  awk '
    /^---$/ {next}
    /^status:/ {in_status=1; next}
    in_status {if (!/^[[:space:]]/) {in_status=0} else {next}}
    /^[[:space:]]+annotations:/ {
        in_annotations=1
        match($0, /^[[:space:]]*/)
        annotations_indent = RLENGTH
        next
    }
    in_annotations {
        match($0, /^[[:space:]]*/)
        current_indent = RLENGTH
        if (current_indent > annotations_indent) {next}
        else {in_annotations=0}
    }
    /^[[:space:]]+creationTimestamp:/ {next}
    {print}
  ' "$file" > "$dest_file"
done

echo "Done."
