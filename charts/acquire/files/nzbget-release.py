"""Removes, with their files, the NZBGet downloads acquire has released.

The chart runs this beside NZBGet, in NZBGet's pod and image, on the same
downloads folder. acquire copies a finished download into the library,
verifies the copy, and then releases the download through NZBGet's API: it
sets the parameter acquire:release=yes on its history record (editqueue
HistorySetParameter). NZBGet's API cannot delete the files of a successful
download — its history delete removes the folder of a failed or deleted one
only — so this loop does: for every record so marked it deletes the folder the
record names, then the record (HistoryFinalDelete). It deletes nothing acquire
did not release, and nothing outside DOWNLOADS/complete; acquire itself never
deletes a file of a client's.

Environment:
  CLIENT_USERNAME, CLIENT_PASSWORD  NZBGet's control login (the chart's Secret)
  CLIENT_PORT                       NZBGet's port in the pod
  DOWNLOADS                         the downloads folder as NZBGet sees it
  RELEASE_INTERVAL                  seconds between passes (default 30)

With --once it makes one pass and exits: non-zero when NZBGet could not be
asked.
"""

import base64
import json
import os
import shutil
import signal
import sys
import time
import urllib.error
import urllib.request

RELEASE = "acquire:release"


def log(msg):
    print("nzbget-release: " + msg, flush=True)


def rpc(method, params):
    port = os.environ.get("CLIENT_PORT", "6789")
    body = json.dumps({"method": method, "params": params, "id": 1}).encode()
    req = urllib.request.Request("http://127.0.0.1:%s/jsonrpc" % port, data=body,
                                 headers={"Content-Type": "application/json"})
    login = "%s:%s" % (os.environ.get("CLIENT_USERNAME", ""), os.environ.get("CLIENT_PASSWORD", ""))
    req.add_header("Authorization", "Basic " + base64.b64encode(login.encode()).decode())
    with urllib.request.urlopen(req, timeout=30) as resp:
        answer = json.load(resp)
    if answer.get("error"):
        raise RuntimeError("%s: %s" % (method, answer["error"].get("message", "error")))
    return answer.get("result")


def within(path, root):
    return path != root and path.startswith(root + os.sep)


def release_pass(complete):
    """One pass over NZBGet's history; returns how many records it released."""
    released = 0
    for item in rpc("history", [False]) or []:
        params = {p.get("Name"): p.get("Value") for p in item.get("Parameters") or []}
        if params.get(RELEASE) != "yes":
            continue
        nzbid = item.get("NZBID")
        folder = item.get("FinalDir") or item.get("DestDir") or ""
        if folder:
            real = os.path.realpath(folder)
            if not within(real, complete):
                log("record %s names %s, outside %s: left alone" % (nzbid, folder, complete))
                continue
            if os.path.isdir(real) and not os.path.islink(folder):
                shutil.rmtree(real)
            elif os.path.lexists(folder):
                os.remove(folder)
        rpc("editqueue", ["HistoryFinalDelete", 0, "", [nzbid]])
        log("released %s: %s removed" % (nzbid, folder or "no folder"))
        released += 1
    return released


def main():
    complete = os.path.realpath(os.path.join(os.environ.get("DOWNLOADS", "/downloads"), "complete"))
    if "--once" in sys.argv:
        try:
            release_pass(complete)
        except (OSError, urllib.error.URLError, RuntimeError, ValueError) as e:
            log("NZBGet could not be asked: %s" % e)
            sys.exit(1)
        return
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
    interval = int(os.environ.get("RELEASE_INTERVAL", "30"))
    failing = False
    while True:
        try:
            release_pass(complete)
            failing = False
        except (OSError, urllib.error.URLError, RuntimeError, ValueError) as e:
            if not failing:  # say it once, not every pass
                log("NZBGet could not be asked: %s" % e)
            failing = True
        time.sleep(interval)


if __name__ == "__main__":
    main()
