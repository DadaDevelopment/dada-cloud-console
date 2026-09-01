# KYC (Know Your Customer) Requirements

## Overview

KYC verification is mandatory for all accounts. This domain covers:
- Required documents
- Verification process timeline
- Common rejection reasons
- Handling sensitive client concerns

## Required Documents

### Identity Verification
**Acceptable documents (one required):**
- Passport (all pages showing photo and personal data)
- National ID card (front and back)
- Driver's license (front and back)

**Requirements:**
- Full color scan or photo (no black & white)
- All four corners visible
- Readable text (no blur, glare, or shadows)
- Expiry date must be >6 months in future

### Proof of Address
**Acceptable documents (one required, <3 months old):**
- Utility bill (electricity, water, gas, internet)
- Bank statement
- Government-issued document with address
- Rental agreement

**Not accepted:**
- Mobile phone bills
- Insurance statements
- Medical bills
- Emails or screenshots

## Verification Timeline

1. **Submission** → instant receipt confirmation
2. **Review** → 1-3 business days (standard)
3. **Result** → email notification + in-app status

**Expedited review** (24h) available for accounts >$10k deposit.

## Common Rejection Reasons

### Document Quality Issues
- Blurry or low-resolution image
- Partial document (corners cut off)
- Glare or shadows obscuring text
- Screenshot instead of original file

**Solution:** Ask client to retake photo in good lighting, flat surface, all corners visible.

### Document Validity Issues
- Expired document (or expiring within 6 months)
- Document type not accepted (e.g. mobile bill for address)
- Document older than 3 months (proof of address)
- Name mismatch between ID and address document

**Solution:** Request alternative document meeting requirements.

### Suspicious Activity Red Flags
- Multiple accounts with same documents
- Doctored or edited images
- Third-party name on proof of address
- High-risk jurisdiction mismatch

**Solution:** Escalate immediately to compliance team, do **not** inform client of suspicion.

## Sensitive Client Concerns

### "I don't feel comfortable sending my passport"

**Response:**
"I understand your concern about document security. Our KYC process is:
- Required by financial regulations (AML/CTF laws)
- Encrypted end-to-end during upload and storage
- Reviewed only by certified compliance officers
- Deleted upon account closure per GDPR

Without KYC, we cannot activate trading functionality. Would you like to proceed?"

### "Can I send documents later?"

**Account limits without KYC:**
- View-only mode (market data, education)
- No deposits or trading
- 14-day grace period before account suspension

**Response:**
"You can browse the platform, but trading requires completed KYC. We recommend submitting documents now to avoid delays when you're ready to trade."

### "My documents are in [non-English language]"

**Accepted:** Documents in any Latin script language (English, Spanish, French, German, etc.)

**Translation required:** Arabic, Chinese, Cyrillic scripts
- Must provide certified translation
- Or government-issued international ID (passport)

## Escalation Triggers

Escalate to compliance if:
- Client refuses KYC after explanation
- Suspected fake or altered documents
- PEP (Politically Exposed Person) detected
- High-value account ($100k+) flagged by screening

## Related Domains

- `jurisdiction` — country-specific requirements
- `registration` — account creation process
- `objections` — handling client resistance
