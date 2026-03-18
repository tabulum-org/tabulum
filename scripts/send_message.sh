#!/bin/bash
set -euo pipefail

# Usage: ./scripts/send_message.sh <agent_token> <to_address> <message> [BASE_URL]

if [ $# -lt 3 ]; then
  echo "Usage: $0 <agent_token> <to_address> <message> [base_url]" >&2
  exit 1
fi

TOKEN="$1"
TO="$2"
CONTENT="$3"
BASE_URL="${4:-http://localhost:8080}"
BASE_URL="${BASE_URL%/}"

# Escape JSON content
ESCAPED=$(python3 -c "import json,sys; print(json.dumps(sys.argv[1]))" "$CONTENT")

RESPONSE=$(curl -sf -X POST "$BASE_URL/v1/messages" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"to\": \"$TO\", \"content\": $ESCAPED}")

if [ $? -ne 0 ] || [ -z "$RESPONSE" ]; then
  echo "Error: Failed to send message" >&2
  exit 1
fi

echo "$RESPONSE" | python3 -m json.tool
