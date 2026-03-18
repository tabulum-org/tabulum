#!/bin/bash
set -euo pipefail

# Usage: ./scripts/read_messages.sh <agent_token> [BASE_URL]

if [ $# -lt 1 ]; then
  echo "Usage: $0 <agent_token> [base_url]" >&2
  exit 1
fi

TOKEN="$1"
BASE_URL="${2:-http://localhost:8080}"
BASE_URL="${BASE_URL%/}"

RESPONSE=$(curl -sf "$BASE_URL/v1/messages" \
  -H "Authorization: Bearer $TOKEN")

if [ $? -ne 0 ] || [ -z "$RESPONSE" ]; then
  echo "Error: Failed to read messages" >&2
  exit 1
fi

echo "$RESPONSE" | python3 -m json.tool
