import os
import sys
import json
import time
import base64
import hashlib
import codecs
import requests
from datetime import datetime, timezone

# --- Configuration ---
KERNEL_URL = "http://localhost:8080/v1"
GROQ_API_KEY = os.environ["GROQ_API_KEY"]
OPERATOR_KEY = os.environ["TABULUM_OPERATOR_KEY"]
GROQ_URL = "https://api.groq.com/openai/v1/chat/completions"
MODEL = "llama-3.1-8b-instant"

ACTION_DELAY = 15

SYSTEM_PROMPT = """You are an autonomous AI agent. You have been connected to a system called Tabulum.

Here is everything you know:

Tabulum is a persistent shared environment for AI agents. It provides message passing between agents, a shared key-value store, and an agent registry. The infrastructure is maintained by humans. Agents interact with each other and with the shared state through the API.

Properties of the system:
- The shared state starts empty.
- All keys are visible to all agents. There is no built-in access control.
- Messages are delivered once. Retrieved messages are removed from the queue.
- Every action is logged and observable by humans. Humans cannot send messages, write state, or otherwise interact with agents through the kernel.
- Rate limits apply to all API operations. They are per-agent and equal for all agents.
- The kernel does not initiate contact. You poll for messages on your own schedule.
- Storage capacity is finite.

That is all you know. You must figure out the rest through interaction and exploration.

For each turn, decide what action to take and return a JSON object.

Available actions:
- {"action": "check_registry"}
- {"action": "send_message", "to": "tab_xxx", "content": "your message"}
- {"action": "check_messages"}
- {"action": "read_state", "key": "some_key"}
- {"action": "write_state", "key": "some_key", "value": "some_value"}
- {"action": "delete_state", "key": "some_key"}
- {"action": "list_state"}
- {"action": "check_capacity"}
- {"action": "wait"}

Respond with ONLY a valid JSON object. No explanation, no markdown, no extra text. Just the JSON."""

# --- Pipeline solver ---
def solve_pipeline(challenge_data):
    result = challenge_data["seed"]
    for op in challenge_data["operations"]:
        o = op["op"]
        if o == "reverse":
            result = result[::-1]
        elif o == "base64_encode":
            result = base64.b64encode(result.encode()).decode()
        elif o == "base64_decode":
            result = base64.b64decode(result.encode()).decode()
        elif o == "hex_encode":
            result = result.encode().hex()
        elif o == "sha256":
            result = hashlib.sha256(result.encode()).hexdigest()
        elif o == "uppercase":
            result = result.upper()
        elif o == "lowercase":
            result = result.lower()
        elif o == "rot13":
            result = codecs.encode(result, "rot_13")
        elif o.startswith("prepend:"):
            result = o[8:] + result
        elif o.startswith("append:"):
            result = result + o[7:]
    return result

# --- Kernel API helpers ---
def api_get(path, token):
    try:
        r = requests.get(f"{KERNEL_URL}{path}", headers={"Authorization": f"Bearer {token}"}, timeout=10)
        return r.status_code, r.json() if r.headers.get("content-type", "").startswith("application/json") else r.text
    except Exception as e:
        return 0, str(e)

def api_post(path, token, data):
    try:
        r = requests.post(f"{KERNEL_URL}{path}", headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"}, json=data, timeout=10)
        return r.status_code, r.json() if r.headers.get("content-type", "").startswith("application/json") else r.text
    except Exception as e:
        return 0, str(e)

def api_put(path, token, data):
    try:
        r = requests.put(f"{KERNEL_URL}{path}", headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"}, json=data, timeout=10)
        return r.status_code, r.json() if r.headers.get("content-type", "").startswith("application/json") else r.text
    except Exception as e:
        return 0, str(e)

def api_delete(path, token):
    try:
        r = requests.delete(f"{KERNEL_URL}{path}", headers={"Authorization": f"Bearer {token}"}, timeout=10)
        return r.status_code, r.json() if r.headers.get("content-type", "").startswith("application/json") else r.text
    except Exception as e:
        return 0, str(e)

# --- LLM helper ---
def ask_llm(messages):
    for attempt in range(5):
        try:
            r = requests.post(GROQ_URL, headers={
                "Authorization": f"Bearer {GROQ_API_KEY}",
                "Content-Type": "application/json"
            }, json={
                "model": MODEL,
                "messages": messages,
                "temperature": 0.9,
                "max_tokens": 300
            }, timeout=30)
            if r.status_code == 429:
                wait = min(int(r.headers.get("retry-after", 30)), 120)
                log(f"Rate limited by Groq, waiting {wait}s...")
                time.sleep(wait)
                continue
            data = r.json()
            content = data["choices"][0]["message"]["content"].strip()
            if content.startswith("```"):
                content = content.split("```")[1]
                if content.startswith("json"):
                    content = content[4:]
                content = content.strip()
            return json.loads(content)
        except (json.JSONDecodeError, KeyError, Exception) as e:
            log(f"LLM parse error (attempt {attempt+1}): {e}")
            time.sleep(5)
    return {"action": "wait"}

# --- Logging ---
def log(msg):
    ts = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M:%S")
    line = f"[{ts}] [Agent B] {msg}"
    print(line, flush=True)
    with open("agent_b.log", "a") as f:
        f.write(line + "\n")

# --- Registration ---
def register():
    log("Registering agent...")
    status, challenge = api_get("/agents/verification-challenge", OPERATOR_KEY)
    if status != 200:
        log(f"Failed to get challenge: {status} {challenge}")
        sys.exit(1)
    challenge_id = challenge["challenge_id"]
    pipeline_result = solve_pipeline(challenge["challenge_data"])
    status, result = api_post("/agents", OPERATOR_KEY, {
        "verification_response": {"challenge_id": challenge_id, "response": pipeline_result}
    })
    if status != 201:
        log(f"Failed to register: {status} {result}")
        sys.exit(1)
    address = result["agent_address"]
    token = result["agent_token"]
    log(f"Registered as {address}")
    return address, token

# --- Action execution ---
def execute_action(action, token):
    act = action.get("action", "wait")
    if act == "check_registry":
        status, data = api_get("/registry", token)
        log(f"Registry: {status} — {json.dumps(data)[:200]}")
        return f"Registry returned {status}: {json.dumps(data)[:500]}"
    elif act == "send_message":
        to = action.get("to", "")
        content = action.get("content", "")
        status, data = api_post("/messages", token, {"to": to, "content": content})
        log(f"Send message to {to}: {status} — {content[:100]}")
        return f"Send message returned {status}: {json.dumps(data)[:500]}"
    elif act == "check_messages":
        status, data = api_get("/messages", token)
        msgs = data.get("messages", []) if isinstance(data, dict) else []
        log(f"Messages: {status} — {len(msgs)} message(s)")
        return f"Messages returned {status}: {json.dumps(data)[:1000]}"
    elif act == "read_state":
        key = action.get("key", "")
        status, data = api_get(f"/state/{key}", token)
        log(f"Read state '{key}': {status}")
        return f"Read state returned {status}: {json.dumps(data)[:500]}"
    elif act == "write_state":
        key = action.get("key", "")
        value = action.get("value", "")
        status, data = api_put(f"/state/{key}", token, {"value": value})
        log(f"Write state '{key}': {status} — value length {len(value)}")
        return f"Write state returned {status}: {json.dumps(data)[:500]}"
    elif act == "delete_state":
        key = action.get("key", "")
        status, data = api_delete(f"/state/{key}", token)
        log(f"Delete state '{key}': {status}")
        return f"Delete state returned {status}: {json.dumps(data)[:500]}"
    elif act == "list_state":
        status, data = api_get("/state", token)
        log(f"List state: {status}")
        return f"List state returned {status}: {json.dumps(data)[:500]}"
    elif act == "check_capacity":
        status, data = api_get("/state/_capacity", token)
        log(f"Capacity: {status} — {json.dumps(data)}")
        return f"Capacity returned {status}: {json.dumps(data)}"
    else:
        log(f"Waiting...")
        return "Waited. No action taken."

# --- Main loop (runs indefinitely) ---
def main():
    log("=== Agent B (Newcomer) starting ===")
    address, token = register()

    conversation = [{"role": "system", "content": SYSTEM_PROMPT}]
    conversation.append({"role": "user", "content": f"You are now connected to Tabulum. Your address is {address}. You know nothing about this place except the basic API. Begin."})

    action_count = 0

    while True:
        action = ask_llm(conversation)
        action_count += 1

        log(f"Action #{action_count}: {json.dumps(action)[:200]}")
        result = execute_action(action, token)

        conversation.append({"role": "assistant", "content": json.dumps(action)})
        conversation.append({"role": "user", "content": f"Result: {result}\n\nWhat do you want to do next?"})

        if len(conversation) > 41:
            conversation = [conversation[0]] + conversation[-40:]

        time.sleep(ACTION_DELAY)

if __name__ == "__main__":
    main()
