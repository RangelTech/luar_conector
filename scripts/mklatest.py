import hashlib
import json
import sys

version = sys.argv[1]
files = {}
for t, ext in (("linux-amd64", ""), ("linux-arm64", ""), ("windows-amd64", ".exe")):
    name = f"luar-conector-{t}{ext}"
    data = open(name, "rb").read()
    files[t] = {"path": name, "sha256": hashlib.sha256(data).hexdigest()}
json.dump({"version": version, "files": files}, open("latest.json", "w"))
