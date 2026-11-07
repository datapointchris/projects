#!/usr/bin/env bash
# Stands in for $EDITOR: appends one line to the file it is handed, then exits.
printf 'Written from the editor.\n' >>"$1"
