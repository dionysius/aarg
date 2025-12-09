# ⚠️ WARNING - DO NOT USE THESE KEYS IN PRODUCTION

**These example keys are publicly available and NOT SECURE!**

Anyone can access these keys since they are committed to the public repository. Using them in production would allow anyone to sign packages and compromise your repository security.

## What to do instead

Use one of your own keys or generate your own GPG keypair for signing your APT repository:

```bash
gpg --full-generate-key
```

After generating your keypair, export it:

```bash
# Export private key (keep this secure!)
gpg --armor --export-secret-keys YOUR_KEY_ID > signing-private.asc

# Export public key (distribute this to users)
gpg --armor --export YOUR_KEY_ID > signing-public.asc
```

Then update your `config.yaml` to point to your new keys.
