import json


def to_json(summary):
    return json.dumps(summary, sort_keys=True, separators=(",", ":"))


def from_json(text):
    return json.loads(text)
