#!/usr/bin/env python3
"""Repack a Helm chart archive so the same chart always gives the same bytes.

helm package stamps every file with the time it ran, so packaging one chart
twice gives two digests. This writes the same files again in name order, owned
by root, dated to one given time, and gzipped without a name or timestamp. The
chart workflow relies on it to tell a re-run (same digest, nothing to publish)
from a change to a version that is already out (different digest, refused).

usage: repack-chart.py IN.tgz OUT.tgz MTIME
  MTIME  seconds since the epoch, e.g. the commit time: git log -1 --format=%ct
"""

import gzip
import io
import sys
import tarfile


def repack(src: str, dst: str, mtime: int) -> None:
    files = []
    with tarfile.open(src, "r:gz") as archive:
        for member in archive.getmembers():
            if member.isdir():
                continue  # implied by the file paths
            if not member.isfile():
                raise SystemExit(f"{src}: {member.name} is not a regular file")
            files.append((member.name, archive.extractfile(member).read()))
    files.sort()

    with open(dst, "wb") as raw, gzip.GzipFile(
        filename="", mode="wb", fileobj=raw, compresslevel=9, mtime=0
    ) as zipped, tarfile.open(
        fileobj=zipped, mode="w", format=tarfile.USTAR_FORMAT
    ) as archive:
        for name, data in files:
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = 0o644
            info.mtime = mtime
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            archive.addfile(info, io.BytesIO(data))


if __name__ == "__main__":
    if len(sys.argv) != 4:
        raise SystemExit(__doc__)
    repack(sys.argv[1], sys.argv[2], int(sys.argv[3]))
