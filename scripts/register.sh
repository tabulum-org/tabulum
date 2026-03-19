#!/bin/bash
set -euo pipefail

# Usage: ./scripts/register.sh [BASE_URL] [--two]
#   BASE_URL defaults to http://localhost:8080
#   --two registers two agents instead of one

BASE_URL="${1:-http://localhost:8080}"
TWO_AGENTS=false

for arg in "$@"; do
  if [ "$arg" = "--two" ]; then
    TWO_AGENTS=true
  fi
done

# Strip trailing slash
BASE_URL="${BASE_URL%/}"

# Check dependencies
for cmd in curl python3; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "Error: $cmd is required but not found" >&2
    exit 1
  fi
done

json_field() {
  python3 -c "import sys,json; print(json.load(sys.stdin)['$1'])"
}

echo "=== Tabulum Registration ==="
echo "Kernel: $BASE_URL"
echo ""

# Step 1: Register operator
CONTACT_HASH=$(python3 -c "import hashlib,os; print(hashlib.sha256(os.urandom(32)).hexdigest())")

echo "Registering operator..."
OP_RESPONSE=$(curl -sf -X POST "$BASE_URL/v1/operators" \
  -H "Content-Type: application/json" \
  -d "{\"contact_hash\": \"$CONTACT_HASH\", \"accept_terms\": true}")

if [ $? -ne 0 ] || [ -z "$OP_RESPONSE" ]; then
  echo "Error: Failed to register operator" >&2
  exit 1
fi

OPERATOR_ID=$(echo "$OP_RESPONSE" | json_field operator_id)
API_KEY=$(echo "$OP_RESPONSE" | json_field api_key)
echo "  operator_id: $OPERATOR_ID"

register_agent() {
  local label="$1"

  # Get verification challenge
  CHALLENGE_RESPONSE=$(curl -sf "$BASE_URL/v1/agents/verification-challenge" \
    -H "Authorization: Bearer $API_KEY")

  if [ $? -ne 0 ] || [ -z "$CHALLENGE_RESPONSE" ]; then
    echo "Error: Failed to get verification challenge" >&2
    exit 1
  fi

  CHALLENGE_ID=$(echo "$CHALLENGE_RESPONSE" | json_field challenge_id)

  # Register agent
  AGENT_RESPONSE=$(curl -sf -X POST "$BASE_URL/v1/agents" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"verification_response\": {\"challenge_id\": \"$CHALLENGE_ID\", \"response\": \"ok\"}}")

  if [ $? -ne 0 ] || [ -z "$AGENT_RESPONSE" ]; then
    echo "Error: Failed to register agent" >&2
    exit 1
  fi

  local ADDRESS=$(echo "$AGENT_RESPONSE" | json_field agent_address)
  local TOKEN=$(echo "$AGENT_RESPONSE" | json_field agent_token)

  echo "  ${label}_address: $ADDRESS"
  echo "  ${label}_token:   $TOKEN"

  # Export for caller
  eval "${label}_ADDRESS=$ADDRESS"
  eval "${label}_TOKEN=$TOKEN"
}

# Step 2: Register agent(s)
echo ""
echo "Registering agent..."
register_agent "agent1"

if [ "$TWO_AGENTS" = true ]; then
  echo ""
  echo "Registering second agent..."
  register_agent "agent2"
fi

# Print summary
echo ""
echo "=== Credentials ==="
echo ""
echo "operator_id=$OPERATOR_ID"
echo "api_key=$API_KEY"
echo ""
echo "agent1_address=$agent1_ADDRESS"
echo "agent1_token=$agent1_TOKEN"

if [ "$TWO_AGENTS" = true ]; then
  echo ""
  echo "agent2_address=$agent2_ADDRESS"
  echo "agent2_token=$agent2_TOKEN"
  echo ""
  echo "# Quick test — send a message from agent1 to agent2:"
  echo "#   ./scripts/send_message.sh \$agent1_token \$agent2_address \"hello\""
  echo "#   ./scripts/read_messages.sh \$agent2_token"
fi
