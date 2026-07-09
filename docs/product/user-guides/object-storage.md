# Object Storage (S3-compatible)

> **Doc-vs-code note:** the inventory is accurate here — confirmed directly against the code.
> A created bucket has **no detail page**, no endpoint/access-key/secret shown anywhere in the
> console, cards are **not clickable**, there's **no delete**, and **`ru1` is the only region**
> offered in the create form's dropdown. Nothing to correct; if anything the inventory is a bit
> generous — the region select isn't just "ru1 only by default," it's the sole hardcoded
> `<option>`.

## What it's for

An S3-compatible bucket (backed by Beget) for media, static assets, or backups.

## How to create a bucket

1. Go to **Object Storage** → **Create Bucket**.
2. Fill in:
   - **Resource Name** (lowercase, digits, hyphens — the console-side identifier).
   - **Bucket Name** (the actual S3 bucket name).
   - **Region** — only `ru1` is offered today.
   - **Description** (optional).
   - **App Reference** (optional) — bind it to a specific app, or leave empty for
     environment-level.
   - **Public Access** toggle — allows unsigned/public URLs.
   - **FTP/SFTP Access** toggle — enables the protocol (on by default); the console does not
     show connection details for it (see Gotchas).
3. Submit — you're redirected to Operations to watch it provision.

## Gotchas

- **You will not get the endpoint, access key, or secret from the console.** Once created, a
  bucket card just shows its name, region, and public/app-ref badges — there is nothing to
  click into. Retrieve S3 credentials through whatever out-of-band process your platform
  operator uses (support, a shared secret store, etc.) — this guide can't tell you where,
  because the UI doesn't expose it.
- Toggling **FTP/SFTP Access** only flips a flag; it doesn't reveal an FTP endpoint or
  credentials either.
- Bucket cards are inert — clicking one does nothing.

## Not yet supported

- A detail view showing the S3 endpoint, access key/secret, or an example `aws-cli` /
  SDK snippet.
- Delete.
- Any region besides `ru1`.
- CDN or versioning (sometimes implied by marketing — not present in this UI at all).
