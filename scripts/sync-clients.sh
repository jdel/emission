#!/bin/sh
# Sync internal/client/clients/*.json from upstream Rust source.
# POSIX shell + awk. No bashisms.
#
# Fetches src/clients.rs from the fake-torrent-client crate (Codeberg), parses
# each ClientVersion match arm, writes one JSON file per client into
# internal/client/clients/.

set -eu

SRC_URL="https://codeberg.org/slundi/fake-torrent-client/raw/branch/master/src/clients.rs"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
OUT_DIR="$SCRIPT_DIR/../internal/client/clients"
mkdir -p "$OUT_DIR"

TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

echo "fetching $SRC_URL" >&2
curl -fsSL "$SRC_URL" -o "$TMP"

awk -v outdir="$OUT_DIR" '
# ----- helpers ---------------------------------------------------------------

function hex2(s,   c1, c2, h) {
    h = "0123456789abcdef"
    c1 = index(h, tolower(substr(s, 1, 1))) - 1
    c2 = index(h, tolower(substr(s, 2, 1))) - 1
    if (c1 < 0 || c2 < 0) return -1
    return c1 * 16 + c2
}

# Decode the body of a Rust raw byte-string into a JSON-ready pattern.
# Handles \\\\\\\\ -> \  (regex meta-escape, upstream over-quoted) and
# \\xHH       -> raw byte, then UTF-8-decoded codepoints emitted as \uXXXX.
function decode_pattern(s,   out, i, c, n, b1, b2, cp) {
    # Strip surrounding b'...'
    if (substr(s, 1, 2) == "b\047" && substr(s, length(s), 1) == "\047")
        s = substr(s, 3, length(s) - 3)
    # Build a byte buffer in array bb[1..nb]
    nb = 0
    i = 1
    while (i <= length(s)) {
        c = substr(s, i, 1)
        if (c == "\\" && substr(s, i, 4) == "\\\\\\\\") {
            nb++; bb[nb] = -1   # sentinel: literal backslash
            i += 4; continue
        }
        if (c == "\\" && substr(s, i + 1, 1) == "x") {
            n = hex2(substr(s, i + 2, 2))
            if (n >= 0) { nb++; bb[nb] = n; i += 4; continue }
        }
        # ASCII char (regex chars from source come in as plain text)
        nb++; bb[nb] = -2 - hex2(sprintf("%02x", ord(c)))  # encode literal char marker
        bb[nb] = -1000 - ord(c)
        i += 1
    }
    # Walk bb: combine 0xC2/0xC3 + continuation into a single codepoint.
    out = ""
    i = 1
    while (i <= nb) {
        b1 = bb[i]
        if (b1 == -1) { out = out "\\\\"; i++; continue }   # literal backslash
        if (b1 <= -1000) {
            out = out json_char(-(b1 + 1000))
            i++; continue
        }
        if ((b1 == 194 || b1 == 195) && i < nb) {
            b2 = bb[i + 1]
            if (b2 >= 128 && b2 <= 191) {
                cp = (b1 == 194 ? 128 : 192) + (b2 - 128)
                out = out sprintf("\\u%04x", cp)
                i += 2; continue
            }
        }
        # standalone byte
        if (b1 < 128 && b1 >= 32) out = out sprintf("%c", b1)
        else out = out sprintf("\\u%04x", b1)
        i++
    }
    delete bb
    return out
}

# JSON-escape one literal ASCII char appearing in a pattern or string.
function json_char(b) {
    if (b == 0x22) return "\\\""
    if (b == 0x5C) return "\\\\"
    if (b < 0x20)  return sprintf("\\u%04x", b)
    return sprintf("%c", b)
}

# Build an ord table once.
function init_ord(   i) {
    for (i = 0; i < 256; i++) ord_table[sprintf("%c", i)] = i
}
function ord(c) {
    return ord_table[c]
}

# JSON-escape a whole string (used for non-pattern fields).
function json_string(s,   i, c, b, out) {
    out = ""
    for (i = 1; i <= length(s); i++) {
        c = substr(s, i, 1)
        b = ord(c)
        if (c == "\\") out = out "\\\\"
        else if (c == "\"") out = out "\\\""
        else if (b < 0x20) out = out sprintf("\\u%04x", b)
        else out = out c
    }
    return out
}

# ----- per-arm field extractors ---------------------------------------------

# Find "self.<field> = <RHS>;" inside the current arm body. <RHS> may span
# multiple lines. Returns the trimmed RHS or "" if absent.
function field(body, name,   re, m_start, m_len, rhs, semi) {
    re = "self\\." name "[ \t]*=[ \t]*"
    if (match(body, re) == 0) return ""
    m_start = RSTART + RLENGTH
    # find terminating semicolon (no semicolons inside string literals here)
    rest = substr(body, m_start)
    semi = index(rest, ";")
    if (semi == 0) return ""
    rhs = substr(rest, 1, semi - 1)
    sub(/^[ \t\n]+/, "", rhs)
    sub(/[ \t\n]+$/, "", rhs)
    return rhs
}

# Parse `String::from("...")` or `String::from(r"...")` from RHS.
function parse_str(rhs,   m) {
    if (rhs == "") return ""
    if (match(rhs, /^String::from\(r"/)) {
        return substr(rhs, RLENGTH + 1, length(rhs) - RLENGTH - 2)
    }
    if (match(rhs, /^String::from\("/)) {
        return substr(rhs, RLENGTH + 1, length(rhs) - RLENGTH - 2)
    }
    return ""
}

function parse_opt_str(rhs,   inner) {
    if (rhs == "" || rhs == "None") return ""
    if (match(rhs, /^Some\(/) == 0) return ""
    inner = substr(rhs, RLENGTH + 1, length(rhs) - RLENGTH - 1)
    sub(/^[ \t\n]+/, "", inner); sub(/[ \t\n]+$/, "", inner)
    return parse_str(inner)
}

function parse_opt_int(rhs) {
    if (rhs == "" || rhs == "None") return -1
    if (match(rhs, /^Some\(([0-9]+)\)/) == 0) return -1
    return substr(rhs, RSTART + 5, RLENGTH - 6) + 0
}

function parse_opt_bool(rhs) {
    if (rhs == "" || rhs == "None") return -1
    if (index(rhs, "Some(true)")  > 0) return 1
    if (index(rhs, "Some(false)") > 0) return 0
    return -1
}

function parse_int(rhs) {
    if (rhs == "") return 0
    if (match(rhs, /^[0-9]+/)) return substr(rhs, RSTART, RLENGTH) + 0
    return 0
}

function parse_bool(rhs) { return rhs == "true" ? 1 : 0 }

function map_algo(rhs) {
    if (index(rhs, "::Hash") > 0 && index(rhs, "NoLeadingZero") == 0) return "HASH"
    if (index(rhs, "HashNoLeadingZero") > 0) return "HASH_NO_LEADING_ZERO"
    if (index(rhs, "DigitRangeTransformed") > 0) return "DIGIT_RANGE_TRANSFORMED_TO_HEX_WITHOUT_LEADING_ZEROES"
    if (index(rhs, "RandomPoolWithChecksum") > 0) return "RANDOM_POOL_WITH_CHECKSUM"
    if (index(rhs, "::Regex") > 0) return "REGEX"
    return "HASH"
}

function map_refresh(rhs) {
    if (index(rhs, "TimedOrAfterStartedAnnounce") > 0) return "TIMED_OR_AFTER_STARTED_ANNOUNCE"
    if (index(rhs, "TorrentVolatile")             > 0) return "TORRENT_VOLATILE"
    if (index(rhs, "TorrentPersistent")           > 0) return "TORRENT_PERSISTENT"
    return "NEVER"
}

# ----- emit one profile JSON -------------------------------------------------

function emit_profile(body,   name, key_algo, peer_algo, key_uc, ref_every, path, f) {
    name = parse_str(field(body, "name"))
    if (name == "") return

    key_algo  = map_algo(field(body, "key_algorithm"))
    key_uc    = parse_opt_bool(field(body, "key_uppercase"))
    ref_every = parse_opt_int(field(body, "key_refresh_every"))
    peer_algo = map_algo(field(body, "peer_algorithm"))

    peer_pat = decode_pattern(parse_str(field(body, "peer_pattern")))
    peer_prefix_s = parse_str(field(body, "peer_prefix"))
    excl     = decode_pattern(parse_str(field(body, "encoding_exclusion_pattern")))
    query    = json_string(parse_str(field(body, "query")))
    user_agent = parse_str(field(body, "user_agent"))
    accept     = parse_str(field(body, "accept"))
    accept_enc = parse_str(field(body, "accept_encoding"))
    accept_lng = parse_str(field(body, "accept_language"))
    conn       = parse_opt_str(field(body, "connection"))

    num_want      = parse_int(field(body, "num_want"))
    num_want_stop = parse_int(field(body, "num_want_on_stop"))
    if (num_want == 0) num_want = 200

    path = outdir "/" name ".json"

    # --- write JSON ---
    f = path
    printf("") > f  # truncate

    printf("{\n")                                                                                  >> f
    # keyGenerator
    printf("    \"keyGenerator\": {\n")                                                            >> f
    printf("        \"algorithm\": {\n")                                                           >> f
    printf("            \"type\": \"%s\"", key_algo)                                               >> f
    if (key_algo == "HASH" || key_algo == "HASH_NO_LEADING_ZERO") {
        printf(",\n            \"length\": 8\n")                                                   >> f
    } else if (key_algo == "DIGIT_RANGE_TRANSFORMED_TO_HEX_WITHOUT_LEADING_ZEROES") {
        printf(",\n            \"inclusiveLowerBound\": 1,\n            \"inclusiveUpperBound\": 2147483647\n") >> f
    } else {
        printf("\n")                                                                               >> f
    }
    printf("        },\n")                                                                         >> f
    printf("        \"refreshOn\": \"%s\",\n", map_refresh(field(body, "key_refresh_on")))         >> f
    printf("        \"keyCase\": \"%s\"", key_uc == 1 ? "upper" : (key_uc == 0 ? "lower" : "none")) >> f
    if (ref_every >= 0) printf(",\n        \"refreshEvery\": %d", ref_every)                       >> f
    printf("\n    },\n")                                                                           >> f

    # peerIdGenerator
    printf("    \"peerIdGenerator\": {\n")                                                         >> f
    printf("        \"algorithm\": {\n")                                                           >> f
    printf("            \"type\": \"%s\"", peer_algo)                                              >> f
    if (peer_algo == "REGEX") {
        printf(",\n            \"pattern\": \"%s\"\n", peer_pat)                                  >> f
    } else if (peer_algo == "RANDOM_POOL_WITH_CHECKSUM") {
        printf(",\n            \"pattern\": \"%s\",\n", peer_pat)                                 >> f
        printf("            \"prefix\": \"%s\",\n", json_string(peer_prefix_s))                   >> f
        printf("            \"charactersPool\": \"%s\",\n", peer_pat)                             >> f
        printf("            \"base\": %d\n", length(peer_pat))                                    >> f
    } else {
        printf("\n")                                                                               >> f
    }
    printf("        },\n")                                                                         >> f
    printf("        \"refreshOn\": \"%s\",\n", map_refresh(field(body, "peer_refresh_on")))        >> f
    printf("        \"shouldUrlEncode\": %s\n", parse_bool(field(body, "peer_url_encode")) ? "true" : "false") >> f
    printf("    },\n")                                                                             >> f

    # urlEncoder
    printf("    \"urlEncoder\": {\n")                                                              >> f
    printf("        \"encodingExclusionPattern\": \"%s\",\n", excl)                                >> f
    printf("        \"encodedHexCase\": \"%s\"\n", parse_bool(field(body, "uppercase_encoded_hex")) ? "upper" : "lower") >> f
    printf("    },\n")                                                                             >> f

    # query, numwant
    printf("    \"query\": \"%s\",\n", query)                                                      >> f
    printf("    \"numwant\": %d,\n", num_want)                                                     >> f
    printf("    \"numwantOnStop\": %d,\n", num_want_stop)                                          >> f

    # requestHeaders
    printf("    \"requestHeaders\": [")                                                            >> f
    first = 1
    if (user_agent != "") { printf("%s\n        {\"name\": \"User-Agent\", \"value\": \"%s\"}",       first?"":",", json_string(user_agent)) >> f; first=0 }
    if (accept     != "") { printf("%s\n        {\"name\": \"Accept\", \"value\": \"%s\"}",           first?"":",", json_string(accept))     >> f; first=0 }
    if (accept_enc != "") { printf("%s\n        {\"name\": \"Accept-Encoding\", \"value\": \"%s\"}", first?"":",", json_string(accept_enc)) >> f; first=0 }
    if (accept_lng != "") { printf("%s\n        {\"name\": \"Accept-Language\", \"value\": \"%s\"}", first?"":",", json_string(accept_lng)) >> f; first=0 }
    if (conn       != "") { printf("%s\n        {\"name\": \"Connection\", \"value\": \"%s\"}",       first?"":",", json_string(conn))       >> f; first=0 }
    printf("\n    ]\n}\n")                                                                         >> f
    close(f)
    written++
}

# ----- main: split into arms -------------------------------------------------

BEGIN {
    init_ord()
    written = 0
}

# Concatenate all input into one string then walk through it.
{ src = src $0 "\n" }

END {
    n = length(src)
    i = 1
    while (i <= n) {
        # find next "ClientVersion::WORD => {"
        s = substr(src, i)
        if (match(s, /ClientVersion::[A-Za-z0-9_]+[ \t]*=>[ \t]*\{/) == 0) break
        start_brace = i + RSTART + RLENGTH - 2   # position of "{"
        # find balanced "}"
        depth = 1
        k = start_brace + 1
        while (k <= n && depth > 0) {
            cc = substr(src, k, 1)
            if (cc == "{") depth++
            else if (cc == "}") depth--
            k++
        }
        body = substr(src, start_brace + 1, k - start_brace - 2)
        emit_profile(body)
        i = k
    }
    printf("wrote %d profiles to %s\n", written, outdir) > "/dev/stderr"
}
' "$TMP"
