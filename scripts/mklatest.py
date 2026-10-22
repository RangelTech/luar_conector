import hashlib
import json
import os
import sys

version = sys.argv[1]
files = {}
for t, ext in (("linux-amd64", ""), ("linux-arm64", ""), ("windows-amd64", ".exe")):
    name = f"luar-conector-{t}{ext}"
    data = open(name, "rb").read()
    files[t] = {"path": name, "sha256": hashlib.sha256(data).hexdigest()}
if os.path.isdir("kits"):
    for name in sorted(os.listdir("kits")):
        if name.startswith("kit-"):
            data = open(os.path.join("kits", name), "rb").read()
            key = name.rsplit(".tar.gz", 1)[0] if name.endswith(".tar.gz") else name.rsplit(".zip", 1)[0]
            files[key] = {"path": "kits/" + name, "sha256": hashlib.sha256(data).hexdigest()}
json.dump({"version": version, "files": files}, open("latest.json", "w"))
