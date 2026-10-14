# Bambu Lab Cloud Auth — exhaustive code findings

Repo: `/home/dsh/printer-dashboard/.dsh-worktrees/lucid-forge` (read-only research).
Primary sources read in full: `internal/printers/bambu/auth.go` (780 L),
`internal/printers/bambu/auth_test.go` (1226 L), `cmd/bambu-login/main.go` (645 L),
`internal/config/config.go` (bambu_account parts). Supporting: `internal/printers/bambu/client.go`,
`internal/server/server.go`, `internal/server/onboarding.go`, `internal/camera/tutk/tutk.go`,
`PLAN.md`, `.fleet/board/done/K-090-*.md`.

Every claim below carries a `file:line`. Facts inferred (not literally in code) are marked **[inference]**.

---

## 0. Host inventory (complete)

| Host | Where | Purpose |
|---|---|---|
| `https://api.bambulab.com` | `auth.go:19` (`apiBaseGlobal`) | default cloud API base for every region except `china` |
| `https://api.bambulab.cn` | `auth.go:20` (`apiBaseChina`) | cloud API base when `region == "china"` |
| `us.mqtt.bambulab.com:8883` | `auth.go:761` | cloud MQTT, any region ≠ `china` |
| `cn.mqtt.bambulab.com:8883` | `auth.go:759` | cloud MQTT, `region == "china"` |
| `https://e.bambulab.com` | `cmd/bambu-login/main.go:128,194,557` | browser SSO target whose `localStorage` holds the web JWT |
| `http://127.0.0.1:<ephemeral>` | `main.go:78-127` | local token-capture server (option-2 flow) |

Base URL is chosen once at construction and never re-derived: `NewBambuCloudClient`
(`auth.go:129-139`) — `if region == "china" { baseURL = apiBaseChina }`, everything else
(including `""`, `"global"`, `"europe"`) → global; asserted by `auth_test.go:1026-1057`.
`http.Client` timeout is 30 s (`auth.go:135`); no retries, no proxy config, no custom transport.

There are **no other Bambu hosts** anywhere in the repo (no `mtr.bambulab.com`, no
`developer.bambulab.com`, no S3/bblp endpoints).

---

## 1. Every Bambu cloud HTTP endpoint

Five paths total. Grep of the whole tree for `/v1/` yields exactly these (`auth.go:340,359,374,447,488,502,582,612,660`)
plus doc mentions in `PLAN.md:114,118,121,179`.

### 1.1 `POST /v1/user-service/user/login`
Call sites: `auth.go:340` (Login step 1), `auth.go:374` (Login 2FA re-submit),
`auth.go:447` (`LoginStep1`), `auth.go:502` (`LoginStep2`). Sent via `doJSON`
(**no** Authorization header — `auth.go:676-708`).

Two distinct request-body shapes:

*Password form* — struct `loginRequest` (`auth.go:28-32`), used at `:334-338` and `:442-446`:
```json
{"account": "<email>", "password": "<password>", "apiError": ""}
```
`apiError` is **always the empty string** — no code path ever populates it (it mirrors the
web app's field for reporting a previous API error).

*Verification-code form* — a bare `map[string]string` (`auth.go:370-373`, `auth.go:498-501`):
```json
{"account": "<email>", "code": "<6-digit code>"}
```
Note: the code form sends **no** `password` and **no** `apiError` key.

Response fields consumed (`loginResponse`, `auth.go:34-41`):

| JSON field | Go field | Consumed where |
|---|---|---|
| `success` | `Success bool` | gate at `:351`, `:387`, `:511`; logged `:381` |
| `accessToken` | `AccessToken string` | becomes `c.token` at `:391`, `:459`, `:515`; the *real* success signal |
| `loginType` | `LoginType string` | `== "verifyCode"` triggers 2FA (`:351`, `:458`); in error text `:388` |
| `tfaKey` | `TFAKey string` | parsed only; surfaced to callers via the `*loginResponse` returned by `LoginStep1` (`:482`) and asserted in `auth_test.go:578-580`. **Never used by any production code path** — the code flow never sends it back. |
| `expiresIn` | `ExpiresIn int64` | `if > 0 → tokenExpiry = now + expiresIn s` (`:392-394`, `:460-462`, `:516-518`) |
| `hasToken` | `HasToken bool` | **parsed but never read.** The log line that prints `hasToken=%v` actually prints `lr.AccessToken != ""` (`auth.go:381-382`). |

Success semantics are deliberately loose: *"After 2FA, the API may return an accessToken but
omit the success field. Treat any response with a non-empty accessToken as successful."*
(`auth.go:385-389`). So `success:false` + non-empty `accessToken` = success.

Failure modes: HTTP ≥ 400 → `doJSON` returns `API error %d: %s` with the raw body
(`auth.go:703-705`), wrapped as `login request failed: …` / `2FA login request failed: … (body: %s)`
(`:342`, `:376`, `:449`, `:504`). Bad credentials are recognised by the server-side substring
`"Incorrect account"` — the CLI greps for exactly that (`main.go:61`).

### 1.2 `POST /v1/user-service/user/sendemail/code`
Call sites: `auth.go:359` (inside `Login`), `auth.go:488` (`SendVerificationCode`). Via `doJSON` (no auth).
Request body (`verifyCodeRequest`, `auth.go:43-46`, built `:358` / `:487`):
```json
{"email": "<email>", "type": "codeLogin"}
```
`type` is the hard-coded literal `"codeLogin"` in both call sites; asserted in `auth_test.go:620`.
**The response body is discarded entirely** at both call sites (`if _, err := …`), only transport/HTTP-status
errors matter. Test stub replies `{"success":true}` (`auth_test.go:625`).
Success log: `bambu cloud: 2FA verification code sent to your email.` (`:363`) /
`… sent to email.` (`:491`).

### 1.3 `GET /v1/iot-service/api/user/bind`
Call site: `auth.go:582` (`GetDevices`), via `doAuthJSON` (Bearer required), **no body**.
Response schema in §7. Errors wrapped as `get devices: %w` / `parsing devices response: %w` (`:584,589`).
`GetDevices` is used as the de-facto **token liveness probe** at startup (`server.go:165`) and by the CLI
(`main.go:296`) and onboarding (`onboarding.go:88,142`).

### 1.4 `POST /v1/iot-service/api/user/ttcode`
Call site: `auth.go:612` (`GetTTCode`), via `doAuthJSON`. Body (`auth.go:611`):
```json
{"dev_id": "<printer serial>"}
```
Response schema in §8. **No production caller** — see §8.

### 1.5 `GET /v1/design-user-service/my/preference`
Call site: `auth.go:660` (`getUserID`, private), via `doAuthJSON`, no body.
Only one field is read (`preferenceResponse`, `auth.go:61-63`):
```go
type preferenceResponse struct { UserID json.Number `json:"uid"` }
```
`json.Number` is deliberate: it preserves the raw integer text (Bambu user IDs are 9-digit
integers; decoding into a numeric Go type via float64 would risk precision/format damage)
**[inference on motive; the effect is literal]**. Empty `uid` → `user_id not found in preference
response` (`:668-670`); JSON failure → `parsing preference: %w` (`:666`).
This endpoint is the **only token validator** in the codebase (see §3.4).

---

## 2. Exact HTTP headers sent

Two helpers, and only two:

`doJSON` (unauthenticated) — `auth.go:676-708`:
```
Content-Type: application/json      (auth.go:690)
```
`doAuthJSON` (authenticated) — `auth.go:710-743`:
```
Content-Type: application/json      (auth.go:724)
Authorization: Bearer <token>       (auth.go:725)   // literal "Bearer " + c.token, no scheme variation
```

Findings:
* **No client-type / client-name headers exist anywhere in this repo.** Grep for
  `X-BBL`, `client-type`, `client-name`, `client_type`, `clientType` across `*.go`/`*.md` → zero hits.
  Real Bambu clients send `X-BBL-Client-Type`/`X-BBL-Client-Name`; this code does not, and the
  endpoints demonstrably answer anyway (the fleet runs on it, `K-090` handoff notes).
* **No `User-Agent` override** → Go's default `Go-http-client/1.1` is what Bambu sees.
* **No `Accept` header.**
* `Authorization` is *never* sent on login/sendemail (they use `doJSON`), even when a token is already loaded.
* Tests pin the header exactly: `auth_test.go:518-520` (preference), `:753-756` (`LoginWithToken`),
  `:932-934` (ttcode) — all compare against `"Bearer <token>"`.
* Error surface: any status ≥ 400 → `API error %d: %s` including the raw body (`:703-705`, `:738-740`).
  Note the asymmetry: `doJSON` returns the body *and* the error; `doAuthJSON` returns `nil` body with the error.

---

## 3. The login state machine

### 3.1 `Login(email, password, codeCallback)` — `auth.go:330-427` (one-shot, CLI/server)
1. `POST …/login` with `{account,password,apiError:""}` (`:334-340`).
2. If `!success && loginType == "verifyCode"` (`:351`):
   * dumps the raw response to the log (`:352`);
   * if `codeCallback == nil` → hard fail:
     `2FA required (check your email for verification code). Either provide a code callback, or use a pre-obtained token instead` (`:354`);
   * else `POST …/sendemail/code` `{email, type:"codeLogin"}` (`:358-361`);
   * calls `codeCallback()` to obtain the code (`:364`) — the CLI prompts here;
   * re-submits `POST …/login` with `{account, code}` (`:370-377`);
   * re-unmarshals into the **same** `lr` struct (`:378`) — **[inference/hazard]** because
     `json.Unmarshal` only overwrites keys present in the second body, a `loginType` left over from
     the first response can persist into the post-2FA error/log text.
3. Success gate: `if !success && accessToken == "" → login failed: success=false, loginType=%s, raw=%s` (`:387-389`).
4. `c.token = accessToken`; `expiresIn > 0` → `tokenExpiry = now + expiresIn` (`:391-394`).
5. `getUserID()` (preference). On failure, **fallback: parse `user_id` out of the token as a JWT**
   (`:399-406`) — only works for `eyJ…` tokens.
6. Log lifetime, then `SaveToken()` (failure is only a warning, `:421-424`).

### 3.2 Split flow for the web UI — `auth.go:429-533`
* `LoginStep1(email,password)` (`:441-483`): same password POST; **completes login only if
  `accessToken != "" && loginType != "verifyCode"`** (`:458`); otherwise returns the raw
  `*loginResponse` so the caller can branch. Returns `TFAKey` to the caller (unused).
* `SendVerificationCode(email)` (`:486-493`).
* `LoginStep2(email, code)` (`:497-533`): `{account,code}` POST; failure string is
  `login failed (code rejected)` (`:512`); then preference lookup (no JWT fallback here, unlike `Login`)
  and `SaveToken()`.
* Web wiring: `onboarding.go:47-90` (step 1 → `needCode:true` + redirect `/onboarding/bambu/code`),
  `onboarding.go:117-158` (step 2), state held in `s.onboardingEmail/onboardingCloud`
  (`server.go:51`, mutex-guarded). Region for onboarding comes from config, default `"global"`
  (`onboarding.go:49-52`).

### 3.3 Error strings (verbatim, complete)
`auth.go`: `login request failed: %w` · `parsing login response: %w` ·
`2FA required (check your email for verification code). Either provide a code callback, or use a pre-obtained token instead` ·
`sending verification code: %w` · `2FA login request failed: %w (body: %s)` ·
`parsing 2FA login response: %w` · `login failed: success=false, loginType=%s, raw=%s` ·
`getting user ID after login: %w` · `token rejected by server: %w` ·
`login failed (code rejected)` · `get devices: %w` · `parsing devices response: %w` ·
`get ttcode: %w` · `parsing ttcode response: %w` · `ttcode API error: %s` ·
`parsing preference: %w` · `user_id not found in preference response` ·
`API error %d: %s` · `create token dir: %w` · `marshal token: %w` · `write token file: %w` ·
`read token file: %w` · `parse token file: %w` · `token file has empty fields` ·
`delete token file: %w` · `invalid JWT format: expected 3 parts, got %d` ·
`decode JWT payload: %w` · `parse JWT payload: %w`.
`main.go`: `❌ Login failed: %v` · `💡 If you use Google SSO, choose option 2 instead.` ·
`❌ Token validation failed: %v` · `   The token may be expired or invalid. Try re-extracting.` ·
`❌ No token received.` · `⚠️  That doesn't look like a JWT token (should start with 'eyJ...')`.
Server-side bad-creds marker: substring **`Incorrect account`** (`main.go:61`).

### 3.4 `LoginWithToken(token)` — the SSO / pre-obtained path (`auth.go:302-324`)
```
c.token = token
uid, err := getUserID()          // GET /v1/design-user-service/my/preference, Bearer token
if err → "token rejected by server: %w"
c.userID = uid
log lifetime if TokenExpiry() non-zero
SaveToken()  (warning only)
```
Validation is **purely "does the preference endpoint accept it"** — no local format check, no
signature check, no expiry check. A 401 from preference is the rejection signal
(`auth_test.go:782-798`). It accepts an `eyJ…` web JWT *and* an opaque token equally.
Callers: `server.go:151` (config `bambu_account.token`) and `main.go:275` (browser/manual capture).

`SetTokenFromLogin(token, userID, region)` (`auth.go:537-541`) sets all three without any
validation — used by onboarding save (`onboarding.go:266`).

### 3.5 `EnsureAuthenticated` — `auth.go:546-572`
Refresh helper: if `TokenValid()` → true; else if email+password → `Login(...)`; else log
`token expired, cannot refresh (no credentials configured)` → false.
**Dead code**: no callers (grep: only the definition). Confirmed independently by
`K-090-…md:39-42` ("`EnsureAuthenticated` exists but is unused dead code").

### 3.6 Region handling
* `"china"` → `.cn` API + `cn.mqtt`; anything else → `.com` API + `us.mqtt` (`auth.go:130-133`, `757-762`).
* Config vocabulary is `"global" | "china"` (`config.go:35`); TTCode's own region vocabulary is
  `"us" | "cn"` (`auth.go:655`, `tutk/tutk.go:14`) — two different vocabularies, never mapped.
* **[hazard]** `LoadToken` overwrites `c.region` from the file (`auth.go:214`) but `baseURL` was
  fixed at construction (`auth.go:136-138`). A token file whose `region` disagrees with the config
  region yields HTTP against one region and MQTT against the other, because the MQTT broker is
  derived from `c.cloud.region` at connect time (`client.go:248`).

---

## 4. Token persistence

### 4.1 Path pattern
* `DefaultTokenDir = filepath.Join(os.Getenv("HOME"), ".printer-dashboard")` — package-level var,
  evaluated at init from `$HOME` (not `os.UserHomeDir()`) — `auth.go:24`. With `HOME` unset this
  silently becomes the relative path `.printer-dashboard`.
* `DefaultTokenPath(email)` — `auth.go:148-153`:
  `@` → `_at_`, `.` → `_dot_`, then `~/.printer-dashboard/bambu_token_<sanitized>.json`.
  The comment says *"Use a hash of the email"* — **it is not a hash**, just two substitutions;
  `/`, `..`, spaces and `+` pass through untouched **[hazard: hostile email → path traversal]**.
  Test pins the shape: `bambu_token_user_at_example_dot_com.json` (`auth_test.go:1064-1071`).
* Concrete paths chosen by callers:
  * server: `DefaultTokenPath(cfg.BambuAccount.Email)`, or `~/.printer-dashboard/bambu_token.json`
    when `bambu_account.token` is set (`server.go:134-137`);
  * onboarding save: `DefaultTokenPath("")` → **`bambu_token_.json`** for token-only accounts
    (`onboarding.go:268-271`);
  * CLI login: `DefaultTokenPath(email)` (`main.go:49`);
  * CLI capture: `DefaultTokenPath(email-from-JWT)`, else **`bambu_token_sso.json`** when the token
    isn't a JWT and yields no email (`main.go:268-271`).
* Shared across git worktrees / containers deliberately — `~/.printer-dashboard` is a shared mount
  so workers reuse one cloud session (`K-090-…md`, `README.md:85-86`).

### 4.2 Exact JSON schema written (`auth.go:158-164`, marshalled at `:175-184`)
```json
{
  "token": "<access token>",
  "user_id": "<numeric uid>",
  "region": "global" | "china" | "",
  "email": "<never written>",
  "expires_at": <unix seconds>
}
```
* `email` has `omitempty` and **`SaveToken` never sets it** (`auth.go:175-180` builds only
  Token/UserID/Region/ExpiresAt) — the field exists in the struct (and in tests,
  `auth_test.go:383`) but can never appear in a file this code writes.
  (`K-090-…md:31-33` claims the cache contains `email`; that is inaccurate.)
* `expires_at` is `int64` with `omitempty`, i.e. omitted **only when exactly 0** — negative values
  are written (see §4.4).
* Encoding: `json.MarshalIndent(..., "", "  ")`.
* Permissions: dir `0700` via `os.MkdirAll` (`:172`), file `0600` via `os.WriteFile` (`:185`).
  Non-atomic, no fsync, no locking (noted in `K-090-…md:36-40`).
* `SaveToken` is a no-op when `tokenFile == ""` (`:168-170`); logs `bambu cloud: token saved to %s`.
* Save happens at exactly four login-completion points: `:320` (`LoginWithToken`), `:422` (`Login`),
  `:477` (`LoginStep1`), `:528` (`LoginStep2`) — plus one explicit call from onboarding save
  (`onboarding.go:273`). Never on a timer, never per request.

### 4.3 Load / delete
`LoadToken` (`auth.go:194-219`): no-op `(false,nil)` if unset; missing file → `(false,nil)`;
bad JSON → `parse token file: %w`; **empty `token` or empty `user_id` → error
`token file has empty fields`** (`:209-211`); sets `token`, `userID`, `region`, and
`tokenExpiry` **only if `expires_at > 0`** (`:215-217`).
`DeleteToken` (`auth.go:222-230`): removes the file, ignores `IsNotExist`.
Startup order in `server.go:143-190`: (1) config token → `LoginWithToken`; (2) `LoadToken` →
`TokenValid()` → `GetDevices()` probe, on any failure `DeleteToken()` and fall through;
(3) email/password with `codeCallback == nil` (so **2FA accounts can never auto-login the server**).

### 4.4 Expiry semantics and the `expires_at: -62135596800` artifact
* `TokenExpiry()` (`auth.go:261-271`): stored `tokenExpiry` if non-zero → else `parseJWT(token).exp`
  → else zero `time.Time{}`.
* `TokenValid()` (`auth.go:274-286`): empty token → false; **zero/unknown expiry → `true`**
  (comment: *"assume valid (e.g., opaque token loaded from disk without saved expiry, or old JWT
  that failed to parse)"*); otherwise valid iff `now + 1h < expiry`.
* `TokenLifetimeLeft()` (`:290-296`) → `time.Until(exp)`, or 0 when unknown; can be negative.
* **`-62135596800` explained:** that is `time.Time{}.Unix()` — the Unix seconds of Go's zero time
  `0001-01-01T00:00:00Z`. `SaveToken` writes `ExpiresAt: c.tokenExpiry.Unix()` unconditionally
  (`auth.go:179`), and `omitempty` does not suppress a negative number. So the file says
  `-62135596800` whenever a login completed with **no `expiresIn` in the response** and the token
  was never parsed as a JWT — i.e. exactly the opaque-token case (§5).
  On the next load the value is **discarded** (`if td.ExpiresAt > 0`, `:215`), so `tokenExpiry`
  stays zero → `TokenExpiry()` zero → `TokenValid()` returns `true` unconditionally.
  Net effect: **the dashboard treats this token as never expiring and never proactively refreshes.**
  The only thing that notices a dead token is the startup `GetDevices()` probe (`server.go:165-169`),
  which deletes the file and then tries email/password (which itself fails if 2FA is on).
  Cosmetic fallout: `server.go:176` logs `Saved Bambu token expired (was valid for %s)` with
  `FormatDuration(negative)` → literally `expired` (`auth.go:767-769`).

---

## 5. `AQBK…` vs `eyJ…` — which token is the MQTT password?

**There is exactly one token slot in the client (`c.token`), and it is used for both the HTTP
`Bearer` value and the MQTT password.** `MQTTPassword()` returns `c.token` verbatim
(`auth.go:751-754`); `doAuthJSON` sends `"Bearer " + c.token` (`auth.go:725`). So the MQTT
password is *whatever token the client currently holds*, and the answer depends on provenance:

| Provenance | Shape | Where it comes from | Used as MQTT password? |
|---|---|---|---|
| `POST /v1/user-service/user/login` → `accessToken` (`auth.go:391,459,515`) | **opaque `AQBK…`, 144 chars** (matches the on-disk artifact) | Bambu's user-service login endpoint — the same endpoint the mobile/Studio clients use | **Yes** — this is the token in `bambu_token_<email>.json`, hence the MQTT password is the **login-service access token, not a web JWT** |
| Browser `localStorage` at `e.bambulab.com` (`main.go:182,186,334,589,619`) | **`eyJ…` JWT** (base64 `{"alg":"RS256"…}`) | web SSO (Google) session token, extracted by bookmarklet / console one-liner / manual paste | Yes, but only if you feed it to `LoginWithToken` (`main.go:275`) or put it in `bambu_account.token` (`server.go:151`) |
| `bambu_account.token` in `config.yaml` (`config.go:33`) | either | human-supplied | Yes |

Evidence the code was written for the opaque case:
* `parseJWT` requires 3 dot-separated parts (`auth.go:236-239`) → an `AQBK…` token fails with
  `invalid JWT format: expected 3 parts, got 1`, so `TokenExpiry()` → zero, `TokenValid()` → true.
* Explicit comments: *"Fallback for JWT tokens (old format or if expiry wasn't saved)"* (`:265`),
  *"opaque token loaded from disk without saved expiry"* (`:280-281`),
  *"Try to extract user_id from the token itself **if it's a JWT**"* (`:400`).
* `Login`'s JWT fallback for `user_id` (`:399-406`) is the only place a token's *contents* matter —
  and it is a fallback, not a requirement. For `AQBK…`, `user_id` must come from the preference
  endpoint or from config.
* The CLI's browser/manual paths **hard-reject** an `AQBK…` token: `if !strings.HasPrefix(token,"eyJ")`
  → `⚠️  That doesn't look like a JWT token (should start with 'eyJ...')` and recurses/exits
  (`main.go:342-349`); the capture page's JS does the same (`main.go:619`), and the bookmarklet
  searches `localStorage` for a value `startsWith('eyJ')` (`main.go:182`). So the two token species
  are handled by disjoint paths.
* Consequence for the SSO path: with an `AQBK…` token, `ParseJWT` fails in `validateAndSave`,
  `email` stays `""`, and the file lands at `bambu_token_sso.json` (`main.go:260-271`).
* The CLI banner claims `Token lasts ~3 months.` (`main.go:31`) — nothing in the code verifies or
  enforces that; it is folklore, and for `AQBK…` tokens the code has no expiry information at all.

---

## 6. MQTT broker + credential derivation

`internal/printers/bambu/client.go:247-268` (`Connect`):
```go
broker := "ssl://" + MQTTBroker(c.cloud.region)          // client.go:248
opts := mqtt.NewClientOptions().
  AddBroker(broker).                                      // :254
  SetClientID(fmt.Sprintf("printer-dashboard-%s-%d", c.cfg.ID, time.Now().UnixNano())). // :255
  SetUsername(c.cloud.MQTTUsername()).                    // :256
  SetPassword(c.cloud.MQTTPassword()).                    // :257
  SetTLSConfig(&tls.Config{InsecureSkipVerify: true})     // :258-260  "Bambu's cloud cert may not be in system store"
  … SetAutoReconnect(true) SetMaxReconnectInterval(30s) SetKeepAlive(30s) SetPingTimeout(10s) SetCleanSession(true)
```
* Broker: `MQTTBroker(region)` → `cn.mqtt.bambulab.com:8883` iff `region == "china"`, else
  `us.mqtt.bambulab.com:8883` (`auth.go:757-762`); pinned by `auth_test.go:1077-1098`. The `ssl://`
  scheme is added by the caller, not by `MQTTBroker`.
* Username: `"u_" + c.userID` (`auth.go:745-749`) — the **numeric uid from the preference endpoint**,
  not the email. Test: `u_12345` (`auth_test.go:1100-1111`).
* Password: `c.token` (`auth.go:751-754`) — see §5.
* Log line on success prints the username: `bambu %s: connected to cloud MQTT at %s (user=%s)`
  (`client.go:282`).
* Topics (docs): `device/{dev_id}/report`, `device/{dev_id}/request` (`PLAN.md:139-141`).
* `brokerOverride` is a test-only seam (`client.go:105-108`).

---

## 7. Device/bind response schema

Wire struct `deviceBindResponse` (`auth.go:48-59`) — the complete set of fields the code parses:

| JSON field | Go type | Copied into `DeviceInfo`? | Consumed by production code? |
|---|---|---|---|
| `message` | `string` | no | **no** — never checked (unlike ttcode, which does check it) |
| `devices[]` | array | — | — |
| `devices[].dev_id` | `string` | yes (`:595`) | yes — printer `serial` (`onboarding.go:243`), model map (`server.go:257`), CLI print (`main.go:304`) |
| `devices[].name` | `string` | yes (`:596`) | yes — printer name (`onboarding.go:241`, `main.go:303`) |
| `devices[].online` | `bool` | yes (`:597`) | display only (`onboarding.go:2892`, `main.go:306`). Runtime online state comes from MQTT, not from bind (`client.go:333,419,474`) |
| `devices[].print_status` | `string` | yes (`:598`) | **never read anywhere** (grep: only the struct + copy) |
| `devices[].dev_model_name` | `string` | yes (`:599`) | yes — persisted as `PrinterDef.Model` for camera routing (`onboarding.go:245`, `server.go:257`) |
| `devices[].dev_product_name` | `string` | yes (`:600`) | display only (`main.go:305`, `onboarding.go:2897`) |
| `devices[].dev_access_code` | `string` | yes (`:601`) | **never read anywhere** — LAN camera access codes still come from `PrinterDef.AccessCode` in config (`config.go:47`, `client.go:216-231`) |

Fields the real Bambu bind payload carries that this struct **silently drops**: `dev_series`,
`dev_swver`, `tags`, `connection`, `username`/`access_code` variants, `dev_ip`, `hubs`, `support`
and friends — none are declared, so all are ignored by `encoding/json`.
`DeviceInfo` is the exported mirror (`auth.go:639-647`), same seven fields with the same JSON tags.

---

## 8. The ttcode call

* Call: `POST /v1/iot-service/api/user/ttcode`, Bearer, body `{"dev_id": "<serial>"}`
  (`auth.go:610-612`).
* Response struct `ttCodeResponse` (`auth.go:67-83`) — every field parsed:
  `message`, `code *string`, `error *string`, `ttcode`, `authkey`, `passwd`, `region`, `type`,
  `streams *string`, `peers *string`, `stream_key`, `stream_salt`, `channel_name`, `app_id`,
  `rtm *string`. The pointer types mark fields the real API sometimes returns as `null` or as a
  non-string (defensive unmarshal).
* Success gate: `resp.Message != "success"` → error `ttcode API error: %s`, preferring the `error`
  field's text when non-empty (`auth.go:622-628`). Test: `device not found` (`auth_test.go:976-998`).
* Returned subset (`TTCodeInfo`, `auth.go:651-656`): `TTCode` (TUTK P2P UID), `AuthKey`,
  `Passwd` (documented as *"6-character hex"*), `Region` (*"us or cn"*). Everything else
  (`stream_key`, `stream_salt`, `channel_name`, `app_id`, `type`, `streams`, `peers`, `rtm`) is
  parsed and thrown away — `rtm`/`streams`/`peers` being the WebRTC-ish fields `PLAN.md:181`
  calls "fields already present in API but null".
* Purpose: TUTK (ThroughTek) P2P **remote camera** credentials — the path Bambu Studio/Handy use.
* **Entire chain is unwired dead code**: `GetTTCode` has no non-test caller;
  `CameraManager.StartTUTK` (`internal/camera/bambu_buffer.go:237-267`) has no caller either;
  `tutk.NewSession` on non-linux/amd64 always returns
  `TUTK P2P camera requires linux/amd64 platform with libBambuSource.so`
  (`internal/camera/tutk/tutk_stub.go:9-13`). `tutk.BuildURL` builds
  `bambu:///tutk?uid=…&authkey=…&passwd=…&region=…&device=…&net_ver=01.09.03.01&dev_ver=00.00.00.00&refresh_url=1&cli_id=00000000-…&cli_ver=1.0.0`
  (`internal/camera/tutk/tutk.go:29-64`) for `libBambuSource.so`. `PLAN.md:178-180` states remote
  camera was never implemented.

---

## 9. `cmd/bambu-login/main.go` — every user-facing string, verbatim

**There is no interactive menu.** `main()` (`main.go:22-68`) is a single linear email/password flow
with one flag: `-email` — *"Bambu account email (pre-fill, password always prompted)"* (`main.go:23`).
Banner (`main.go:26-32`):
```
╔══════════════════════════════════════════════════════╗
║      Bambu Lab Cloud Token Utility                  ║
╠══════════════════════════════════════════════════════╣
║  Gets a JWT token for Bambu Cloud printers.         ║
║  No LAN mode or developer mode needed.              ║
║  Token lasts ~3 months.                             ║
╚══════════════════════════════════════════════════════╝
```
Prompts: `Bambu account email: ` · `Bambu account password: ` (echoed, not hidden — `prompt` uses
`bufio` on stdin, `main.go:358-363`) · `Region [global]: ` (default `global`, `main.go:46,365-371`).
Login line: `\nLogging in...`. 2FA prompt inside the callback (`main.go:54-56`):
```
📧 Verification code sent to your email.
   Check inbox (and spam) for the 6-digit code.
Enter code: 
```
Failure path (`main.go:59-65`): `❌ Login failed: %v` then, only if the error contains
`Incorrect account`: `💡 If you use Google SSO, choose option 2 instead.`
**"Option 2" does not exist as a menu choice**: `loginWithBrowser` (`main.go:74-173`) and
`manualTokenEntry` (`main.go:325-352`) are **unreachable dead code** — nothing calls
`loginWithBrowser` (grep: only its own definition and its internal fallback call to
`manualTokenEntry`). It has been dead since the commit that introduced it (`e9b6fed`).

Success output (`printSuccess`, `main.go:284-319`):
```
✅ Token obtained and saved!
   Token expires: %s (in %s)
📋 Bound printers (%d):
   • %s
     Serial: %s
     Model:  %s
     Online: %v
📝 Token saved to disk. The server will auto-load it on restart.

   Add this to config.yaml's bambu_account section:
   user_id: %s
   (token is already on disk — no need to copy it into yaml)

🚀 Start the dashboard:
   go run .
```
Browser-capture path (dead but fully implemented, `main.go:130-149`) prints:
```
🚀 Opening your browser...
╔══════════════════════════════════════════════════════════════╗
║  👆 Check your browser — a capture page should be open.    ║
║  1. Log in at e.bambulab.com with Google SSO               ║
║  2. On the capture page, drag the bookmarklet to your      ║
║     bookmarks bar, then click it on e.bambulab.com         ║
║  3. Or paste this one-liner into the DevTools Console:     ║
║     (F12 → Console → Ctrl+V → Enter)                       ║
║  (already on the capture page — scroll to Step 3)           ║
║  Token will auto-capture — no manual copying needed!       ║
╚══════════════════════════════════════════════════════════════╝
```
then `✅ Token captured from browser! Validating...`. Local server routes: `/` (capture page),
`/callback?token=…` (`main.go:200-214`), `POST /submit` JSON `{token}` or form `token`
(`main.go:217-250`), `/console` (plain-text one-liner), `/health`. Bookmarklet + one-liner both
hunt `localStorage` for a value `startsWith('eyJ')` (`main.go:182,186`).
Manual fallback instructions (`main.go:326-337`), verbatim steps:
```
📋 Step-by-step: Get your token from the browser
  1. Open https://e.bambulab.com in your browser
  2. Sign in with Google (or your usual method)
  3. Open Developer Tools (F12 or Cmd+Option+I)
  4. Go to the Console tab
  5. Copy-paste and run:
     Object.values(localStorage).find(v => v.startsWith('eyJ'))
  6. Copy the result (the long string starting with eyJ)
Paste the JWT token value: 
```
Capture page HTML (`main.go:410-643`) has 4 steps: 1 "Log in to Bambu Lab" (button →
`https://e.bambulab.com`), 2 "Drag this bookmarklet to your bookmarks bar" (yellow
`🔑 Extract Token` link; `Cmd+Shift+B`/`Ctrl+Shift+B` hint), 3 "Or use the Console one-liner",
4 "Manual paste (last resort)" with placeholder `eyJhbGciOiJSUzI1NiIs...` and client-side
`startsWith('eyJ')` validation; footer `⏳ Waiting for token from browser... Once captured, check your terminal.`
`validateAndSave` prints `   Token belongs to: %s` and `   Expires: %s` when the token is a JWT
(`main.go:263-264`), and on rejection `❌ Token validation failed: %v` +
`   The token may be expired or invalid. Try re-extracting.` (`main.go:276-277`).
Browser opener tries `open`, `xdg-open`, `cmd /c start`, else prints
`   Please open this URL in your browser: %s` (`main.go:390-404`).

---

## 10. Cross-cutting hazards worth knowing before touching this code

1. **No expiry tracking for opaque tokens** (§4.4): `expires_at: -62135596800` means "unknown", and
   `TokenValid()` answers `true` for unknown — so a dead `AQBK…` token is only discovered by a live
   `GetDevices()` call at startup (`server.go:165`), and recovery requires working
   email/password **without 2FA** (`server.go:183-189` passes `nil` callback).
2. **`EnsureAuthenticated` is dead** — nothing refreshes a token at runtime; a token that expires
   mid-run stays broken until restart (`auth.go:546-572`).
3. **Region split-brain** between `baseURL` (fixed at construction) and `c.region` (overwritten by
   `LoadToken`) → API and MQTT can end up in different regions (§3.6).
4. **`dev_access_code` from bind is fetched and discarded** — the camera path still needs a
   hand-entered `access_code` in config even though the cloud already handed it over
   (`auth.go:601` vs `config.go:47`).
5. **`DefaultTokenPath` is not a hash** and does not sanitize `/` or `..` (§4.1).
6. **`InsecureSkipVerify: true`** on the cloud MQTT TLS connection (`client.go:258-260`).
7. **`loginType` stickiness** across the two `Login` unmarshals into one struct (§3.1).
8. **`tfaKey` is parsed and returned but never used** — the 2FA flow is email-code only; there is no
   TOTP/authenticator path in this codebase.
9. **`message` on the bind response is never checked** (unlike ttcode's `message == "success"` gate).
10. **CLI option 2 is unreachable** while the error text still tells users to pick it (§9).

## 11. Test-derived facts worth preserving
`auth_test.go` pins: JWT parse accepts RawURL, RawStd and Std base64 (`:134-157`, `auth.go:241-252`);
missing `exp` → `Exp == 0` (`:194-207`); `TokenValid` matrix incl. `<1h → invalid` and
`zero expiry → valid` (`:214-261`); `SaveToken` with unset `tokenFile` is a silent no-op (`:334-340`);
`SaveToken` creates missing parent dirs (`:342-359`); `LoginStep2` performs exactly **2** HTTP calls
(login + preference) (`:716-718`); `LoginWithToken` performs exactly **1** (preference only)
(`:749-762`); unknown region falls back to global for both API and MQTT (`:1052-1057`, `:1092-1097`);
`FormatDuration` outputs `expired` / `1d 1h 0m` / `2h 30m` / `45m` / `0m` (`:1117-1178`).
