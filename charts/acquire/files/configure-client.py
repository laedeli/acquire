"""Sets what the acquire chart owns in a download client's configuration.

The chart runs this as the init container of qBittorrent and NZBGet, in the
client's own image (both images it pins ship python3), on every start: the
clients rewrite their configuration while they run, so what the chart owns is
set again before each start. Only the keys named here change; everything else
in the file stays as the client, or whoever uses it, left it. NZBGet's MainDir
and news servers, and everything else in qBittorrent, are theirs.

What it owns:
  - the downloads folder, on the downloads volume (not the media storage):
    unfinished downloads in DOWNLOADS/incomplete, finished ones in
    DOWNLOADS/complete, where acquire reads them to copy into the library;
  - the room they take: qBittorrent preallocates a torrent whole, NZBGet
    pauses below CLIENT_DISK_SPACE_MB free;
  - qBittorrent's seeding goal (a ratio and a seeding time, whichever comes
    first): there it stops the torrent, and acquire removes it with its data;
  - the login acquire uses, and no access without one;
  - the Web UI / API port the chart's Service targets;
  - qBittorrent's proxy: through the platform VPN's SOCKS5 proxy for torrents,
    peers, trackers and its own downloads, names resolved by the proxy — or no
    proxy at all without the VPN.

Environment:
  CLIENT               qbittorrent | nzbget
  CLIENT_USERNAME      the login acquire uses
  CLIENT_PASSWORD      its password (the chart's Secret)
  CLIENT_PORT          the Web UI / API port
  CLIENT_PROXY         qbittorrent: socks5h://host:port, or empty for no proxy
  CLIENT_SEED_RATIO    qbittorrent: the ratio a torrent seeds to (-1: no limit)
  CLIENT_SEED_MINUTES  qbittorrent: the minutes it seeds at most (-1: no limit)
  CLIENT_DISK_SPACE_MB nzbget: free space below which it pauses (DiskSpace)
  CONFIG_DIR           the client's configuration volume
  DOWNLOADS            the downloads folder as the client sees it
"""

import base64
import binascii
import hashlib
import hmac
import os
import sys
import tempfile
import urllib.parse


def fail(msg):
    print("configure-client: " + msg, file=sys.stderr)
    sys.exit(1)


def env(name):
    value = os.environ.get(name, "")
    check(name, value)
    if value == "":
        fail(name + " is not set")
    return value


def number(name):
    """A ratio from the environment, as qBittorrent writes one; -1 when unset."""
    value = os.environ.get(name, "").strip() or "-1"
    try:
        f = float(value)
    except ValueError:
        fail(name + " must be a number")
    return "-1" if f < 0 else ("%g" % f)


def integer(name, unset=-1):
    value = os.environ.get(name, "").strip()
    if value == "":
        return str(unset)
    try:
        n = int(value)
    except ValueError:
        fail(name + " must be a whole number")
    return str(max(n, -1))


def check(name, value):
    """Refuse what neither configuration format can carry on one line."""
    if any(ord(c) < 0x20 or ord(c) == 0x7F for c in value):
        fail(name + " contains a control character")
    if value != value.strip():
        fail(name + " starts or ends with white space")


def write(path, text, mode):
    """Replace path with text in one step, so a client never reads half a file."""
    fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path), prefix=".configure-")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(text)
            f.flush()
            os.fsync(f.fileno())
        os.chmod(tmp, mode)
        os.replace(tmp, path)
    except BaseException:
        if os.path.exists(tmp):
            os.remove(tmp)
        raise


def read(path, seed=None):
    """A file's text; the image's own first-start file when it is missing."""
    for p in (path, seed):
        if p and os.path.exists(p):
            with open(p, encoding="utf-8") as f:
                return f.read(), p == path
    return "", False


# ── qBittorrent: a QSettings INI file ────────────────────────────────────────


class Ini:
    """A QSettings INI file, edited line by line: what is not set here stays
    exactly as it was. Keys are QSettings keys within a section, written with
    a backslash between their parts (Session\\Port in [BitTorrent])."""

    def __init__(self, text):
        self.lines = text.splitlines()

    def _section(self, name):
        start = None
        for i, line in enumerate(self.lines):
            s = line.strip()
            if s.startswith("[") and s.endswith("]"):
                if start is not None:
                    return start, i
                if s[1:-1] == name:
                    start = i
        return (start, len(self.lines)) if start is not None else (None, None)

    def _key(self, section, key):
        start, end = self._section(section)
        if start is None:
            return None, None, None
        for i in range(start + 1, end):
            k, sep, _ = self.lines[i].partition("=")
            if sep and k.strip() == key:
                return start, end, i
        return start, end, None

    def get(self, section, key):
        _, _, i = self._key(section, key)
        return None if i is None else self.lines[i].partition("=")[2].strip()

    def set(self, section, key, raw):
        start, end, i = self._key(section, key)
        line = key + "=" + raw
        if i is not None:
            self.lines[i] = line
        elif start is not None:
            at = end
            while at > start + 1 and not self.lines[at - 1].strip():
                at -= 1  # before the blank line that ends the section
            self.lines.insert(at, line)
        else:
            if self.lines and self.lines[-1].strip():
                self.lines.append("")
            self.lines += ["[" + section + "]", line]

    def remove(self, section, key):
        _, _, i = self._key(section, key)
        if i is not None:
            del self.lines[i]

    def text(self):
        return "\n".join(self.lines) + "\n"


def qt_bool(value):
    return "true" if value else "false"


def qt_string(value):
    """A string as QSettings writes one: backslash and quote escaped, quoted
    when it holds a list or key separator or edge spaces, @ doubled."""
    out = value.replace("\\", "\\\\").replace('"', '\\"')
    if out.startswith("@"):
        out = "@" + out
    if any(c in out for c in ";,=") or out != out.strip():
        out = '"' + out + '"'
    return out


def qt_unquote(raw):
    if len(raw) >= 2 and raw.startswith('"') and raw.endswith('"'):
        return raw[1:-1].replace('\\"', '"').replace("\\\\", "\\")
    return raw


PBKDF2_ITERATIONS = 100000  # qBittorrent's Utils::Password::PBKDF2


def pbkdf2(password, salt):
    return hashlib.pbkdf2_hmac("sha512", password.encode("utf-8"), salt, PBKDF2_ITERATIONS, 64)


def password_hash(password, stored):
    """WebUI\\Password_PBKDF2 for password: the stored one when it already is
    this password's (a start does not change the file needlessly), else a new
    one in qBittorrent's format, base64(16-byte salt):base64(key)."""
    raw = qt_unquote(stored or "")
    if raw.startswith("@ByteArray(") and raw.endswith(")"):
        try:
            salt_b64, key_b64 = raw[len("@ByteArray("):-1].split(":", 1)
            if hmac.compare_digest(pbkdf2(password, base64.b64decode(salt_b64, validate=True)),
                                   base64.b64decode(key_b64, validate=True)):
                return stored
        except (ValueError, binascii.Error):
            pass
    salt = os.urandom(16)
    value = base64.b64encode(salt).decode() + ":" + base64.b64encode(pbkdf2(password, salt)).decode()
    return '"@ByteArray(' + value + ')"'


def parse_proxy(proxy):
    """host, port of socks5h://host:port (socks5:// is taken the same way:
    names are always resolved by the proxy)."""
    u = urllib.parse.urlsplit(proxy)
    try:
        port = u.port
    except ValueError:
        port = None
    if u.scheme not in ("socks5h", "socks5") or not u.hostname or not port \
            or u.username or u.password or u.path not in ("", "/") or u.query or u.fragment:
        fail("CLIENT_PROXY must be socks5h://host:port")
    return u.hostname, port


# The keys qBittorrent's settings migration copies over their current names
# when a configuration has not been migrated yet (no Meta\MigrationVersion, as
# the image's first-start file): the legacy name of each key set here, removed
# so the migration cannot overwrite the value (src/app/upgrade.cpp,
# migrateSettingKeys, for MigrationVersion < 2).
QBT_LEGACY = {
    ("BitTorrent", "Session\\DefaultSavePath"): ("Preferences", "Downloads\\SavePath"),
    ("BitTorrent", "Session\\TempPath"): ("Preferences", "Downloads\\TempPath"),
    ("BitTorrent", "Session\\TempPathEnabled"): ("Preferences", "Downloads\\TempPathEnabled"),
    ("BitTorrent", "Session\\ProxyPeerConnections"): ("Preferences", "Connection\\ProxyPeerConnections"),
    ("BitTorrent", "Session\\LSDEnabled"): ("Preferences", "Bittorrent\\LSD"),
    ("Network", "PortForwardingEnabled"): ("Preferences", "Connection\\UPnP"),
    ("Network", "Proxy\\IP"): ("Preferences", "Connection\\Proxy\\IP"),
    ("Network", "Proxy\\Port"): ("Preferences", "Connection\\Proxy\\Port"),
    ("Network", "Proxy\\Type"): ("Preferences", "Connection\\ProxyType"),
    ("BitTorrent", "Session\\Preallocation"): ("Preferences", "Downloads\\PreAllocation"),
    ("BitTorrent", "Session\\GlobalMaxRatio"): ("Preferences", "Bittorrent\\MaxRatio"),
    ("BitTorrent", "Session\\ShareLimitAction"): ("Preferences", "Bittorrent\\MaxRatioAction"),
}


def configure_qbittorrent(username, password, port, downloads):
    conf_dir = os.path.join(env("CONFIG_DIR"), "qBittorrent")
    conf = os.path.join(conf_dir, "qBittorrent.conf")
    pending = os.path.join(conf_dir, "qBittorrent_new.conf")
    os.makedirs(conf_dir, exist_ok=True)
    # qBittorrent writes qBittorrent_new.conf and renames it over its file; one
    # left behind by an unclean exit is what it would restore at start (when it
    # holds anything), so settle it the same way first.
    if os.path.exists(pending):
        if os.path.getsize(pending) > 0:
            os.replace(pending, conf)
        else:
            os.remove(pending)
    # The image's entrypoint copies its first-start file when there is none.
    text, existed = read(conf, "/defaults/qBittorrent.conf")
    ini = Ini(text)

    ratio, minutes = number("CLIENT_SEED_RATIO"), integer("CLIENT_SEED_MINUTES")
    keys = [
        # Downloads: unfinished under incomplete, finished under complete, the
        # folder the gateway also names with every add.
        ("BitTorrent", "Session\\DefaultSavePath", qt_string(downloads + "/complete")),
        ("BitTorrent", "Session\\TempPath", qt_string(downloads + "/incomplete")),
        ("BitTorrent", "Session\\TempPathEnabled", qt_bool(True)),
        # A torrent takes its whole size when it is added, so the volume never
        # holds more than it can finish.
        ("BitTorrent", "Session\\Preallocation", qt_bool(True)),
        # The seeding goal: stopped at the ratio or the seeding time, whichever
        # comes first; acquire then removes it with its data.
        ("BitTorrent", "Session\\GlobalMaxRatio", ratio),
        ("BitTorrent", "Session\\GlobalMaxSeedingMinutes", minutes),
        ("BitTorrent", "Session\\ShareLimitAction", "Stop"),
        # The login, and no way around it: not from localhost (a port-forward
        # arrives there) and not from a whitelisted subnet.
        ("Preferences", "WebUI\\Username", qt_string(username)),
        ("Preferences", "WebUI\\Password_PBKDF2", password_hash(password, ini.get("Preferences", "WebUI\\Password_PBKDF2"))),
        ("Preferences", "WebUI\\LocalHostAuth", qt_bool(True)),
        ("Preferences", "WebUI\\AuthSubnetWhitelistEnabled", qt_bool(False)),
        ("Preferences", "WebUI\\Address", qt_string("*")),
        ("Preferences", "WebUI\\Port", str(port)),
    ]
    proxy = os.environ.get("CLIENT_PROXY", "").strip()
    if proxy:
        host, proxy_port = parse_proxy(proxy)
        keys += [
            # Everything through the SOCKS5 proxy (src/base/net/
            # proxyconfigurationmanager.cpp, preferences.cpp): torrents, RSS
            # and qBittorrent's own downloads, names resolved by the proxy.
            ("Network", "Proxy\\Type", "SOCKS5"),
            ("Network", "Proxy\\IP", qt_string(host)),
            ("Network", "Proxy\\Port", str(proxy_port)),
            ("Network", "Proxy\\AuthEnabled", qt_bool(False)),
            ("Network", "Proxy\\HostnameLookupEnabled", qt_bool(True)),
            ("Network", "Proxy\\Profiles\\BitTorrent", qt_bool(True)),
            ("Network", "Proxy\\Profiles\\RSS", qt_bool(True)),
            ("Network", "Proxy\\Profiles\\Misc", qt_bool(True)),
            # Peer connections too (libtorrent proxy_peer_connections; tracker
            # connections are proxied by libtorrent's default, UDP through
            # the proxy's UDP association or not at all).
            ("BitTorrent", "Session\\ProxyPeerConnections", qt_bool(True)),
            # Nothing that talks to the local network or a router directly,
            # and no peer name lookups outside the proxy.
            ("BitTorrent", "Session\\LSDEnabled", qt_bool(False)),
            ("Network", "PortForwardingEnabled", qt_bool(False)),
            ("Preferences", "Connection\\ResolvePeerHostNames", qt_bool(False)),
        ]
    else:
        keys += [("Network", "Proxy\\Type", "None")]

    try:
        migrated = int(ini.get("Meta", "MigrationVersion") or 0)
    except ValueError:
        migrated = 0
    for section, key, raw in keys:
        if migrated < 2 and (section, key) in QBT_LEGACY:
            ini.remove(*QBT_LEGACY[(section, key)])
        ini.set(section, key, raw)
    if migrated < 7:
        # The migration to version 7 sets ShareLimitAction from this legacy key
        # (migrateShareLimitActionSettings).
        ini.remove("BitTorrent", "Session\\MaxRatioAction")
    if proxy and migrated < 6:
        # The migration to version 6 sets Proxy\HostnameLookupEnabled from
        # this legacy key and drops it, and the profiles from OnlyForTorrents
        # (migrateProxySettings): give it what is set above.
        ini.set("BitTorrent", "Session\\ProxyHostnameLookup", qt_bool(True))
        ini.remove("Network", "Proxy\\OnlyForTorrents")
        ini.remove("Preferences", "Connection\\ProxyOnlyForTorrents")

    write(conf, ini.text(), 0o600)
    print("configure-client: qbittorrent: %d keys set in %s%s, proxy %s" % (
        len(keys), conf, "" if existed else " (new)", ("%s:%d" % (host, proxy_port)) if proxy else "none"))


# ── NZBGet: Name=Value lines ─────────────────────────────────────────────────


def nzb_set(lines, key, value):
    """Set the first uncommented Key=, or add it at the end."""
    for i, line in enumerate(lines):
        k, sep, _ = line.partition("=")
        if sep and not line.lstrip().startswith("#") and k.strip().lower() == key.lower():
            lines[i] = key + "=" + value
            return
    lines.append(key + "=" + value)


# What the image's entrypoint changes in its template when it creates the
# file on the first start; the file is created here instead, before it runs.
NZB_FIRST_START = [
    ("MainDir", "/config"),
    ("ScriptDir", "${MainDir}/scripts"),
    ("WebDir", "${AppDir}/webui"),
    ("ConfigTemplate", "${AppDir}/webui/nzbget.conf.template"),
    ("UnrarCmd", "unrar"),
    ("SevenZipCmd", "7z"),
    ("LogFile", "${MainDir}/nzbget.log"),
    ("UpdateCheck", "none"),
    ("WriteLog", "none"),
]


def configure_nzbget(username, password, port, downloads):
    if os.environ.get("CLIENT_PROXY", "").strip():
        fail("NZBGet has no SOCKS5 proxy: it cannot go through the VPN")
    conf = os.path.join(env("CONFIG_DIR"), "nzbget.conf")
    text, existed = read(conf, "/app/nzbget.conf")
    lines = text.splitlines()
    if not existed:
        for key, value in NZB_FIRST_START:
            nzb_set(lines, key, value)
    keys = [
        # Downloads: unfinished ones (and their repair) in the intermediate
        # folder; the finished folder, where NZBGet also unpacks, in DestDir.
        ("DestDir", downloads + "/complete"),
        ("InterDir", downloads + "/incomplete"),
        # Paused before the volume runs low (checked for both folders).
        ("DiskSpace", str(integer("CLIENT_DISK_SPACE_MB", 0))),
        # The control login acquire uses, on the port the Service targets,
        # and no address that gets in without it.
        ("ControlIP", "0.0.0.0"),
        ("ControlPort", str(port)),
        ("ControlUsername", username),
        ("ControlPassword", password),
        ("AuthorizedIP", ""),
    ]
    for key, value in keys:
        nzb_set(lines, key, value)
    write(conf, "\n".join(lines) + "\n", 0o600)
    print("configure-client: nzbget: %d keys set in %s%s" % (len(keys), conf, "" if existed else " (new)"))


def main():
    client = env("CLIENT")
    username, password = env("CLIENT_USERNAME"), env("CLIENT_PASSWORD")
    port = env("CLIENT_PORT")
    if not port.isdigit() or not 0 < int(port) < 65536:
        fail("CLIENT_PORT must be a port number")
    downloads = env("DOWNLOADS").rstrip("/")
    if not downloads.startswith("/"):
        fail("DOWNLOADS must be an absolute path")

    # The client's folders on the downloads volume, group-writable: acquire
    # reads the finished ones.
    os.umask(0o002)
    for sub in ("incomplete", "complete"):
        os.makedirs(os.path.join(downloads, sub), exist_ok=True)

    if client == "qbittorrent":
        configure_qbittorrent(username, password, int(port), downloads)
    elif client == "nzbget":
        configure_nzbget(username, password, int(port), downloads)
    else:
        fail("CLIENT must be qbittorrent or nzbget")


if __name__ == "__main__":
    main()
