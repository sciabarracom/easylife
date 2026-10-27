# Trustant licence format

A licence is a text token signed with Ed25519. The shop worker signs it
(`cloudflare/lib/license.js`, called by `licenseFor` in
`cloudflare/shop/index.js`); the app verifies it (`app/license.go`).

## Token

```
lic_<base64url(payload JSON)>.<base64url(signature)>
```

- prefix `lic_`;
- two parts separated by `.`, both base64url without padding;
- part 1: the exact bytes of the JSON payload;
- part 2: the 64-byte Ed25519 signature of those bytes.

## Payload

```json
{
  "v": 1,
  "sub": "buyer@example.com",
  "email": "buyer@example.com",
  "subscription": "sub_…",
  "uid": "<order uid>",
  "hosts": ["http://<name>.n7s.co"],
  "iat": "2026-10-26",
  "exp": "2027-10-26"
}
```

| field | meaning | used by the app |
|---|---|---|
| `v` | format version, 1 | yes |
| `sub` | buyer email, lower case | yes |
| `hosts` | licensed hosts, scheme + host | yes |
| `iat` | issue date, `YYYY-MM-DD` | shown only |
| `exp` | end of the paid Stripe period, `YYYY-MM-DD` | yes |
| `email` | buyer email (spec 60) | ignored |
| `subscription` | Stripe subscription id (spec 60) | ignored |
| `uid` | order uid, for `/v1/update` and support (spec 61) | ignored |

Each renewal signs a new licence with the new `exp`; the current one is kept
in the subscription's `metadata.license`.

## Decoding

```bash
LIC=lic_...
echo "${LIC#lic_}" | cut -d. -f1 | python3 -c 'import sys,base64;s=sys.stdin.read().strip();sys.stdout.buffer.write(base64.urlsafe_b64decode(s+"="*(-len(s)%4)))' | jq .
```

## Validation (`validateLicense`, app/license.go)

1. Trim, require the `lic_` prefix and exactly two `.`-separated parts.
2. base64url-decode both; the signature must be 64 bytes.
3. `ed25519.Verify(pub, payloadBytes, sig)`, with `pub` the embedded
   `master_key_pub` (base64, 32 bytes). The signature covers the original
   payload bytes, never a re-serialized JSON.
4. Parse the JSON; unknown fields are ignored, so new fields do not break
   installed apps.
5. Expiry: no `exp` never expires; an unreadable `exp` is invalid; otherwise
   valid through the whole `exp` day.
6. Host check at deploy (`requireLicensedHost`): the apihost, normalized to
   lower-case `scheme://host[:port]`, must equal one entry of `hosts`; no
   wildcards. `localhost`, `127.0.0.1`, `::1` and `miniops.me` are always
   allowed.

Failures answer HTTP 402 with `License required: …` or `Host not licensed: …`.

## Signing (worker)

`MASTER_KEY_BASE64` is Go's 64-byte Ed25519 private key in base64. The worker
takes its first 32 bytes (the seed), prepends the fixed PKCS#8 header, imports
it into WebCrypto and signs the bytes of `JSON.stringify(payload)`.
