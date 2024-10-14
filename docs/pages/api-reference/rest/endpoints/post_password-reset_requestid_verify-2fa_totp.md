---
title: "POST /password-reset/[request_id]/verify-2fa/totp"
---

# POST /password-reset/[request_id]/verify-2fa/totp

Verifies the TOTP code of a password reset request's user and marks the password reset as 2FA-verfied. The user will be locked out from using TOTP as their second factor for 15 minutes after their 5th consecutive failed attempts.

```
POST https://your-domain.com/password-reset/REQUEST_ID/verify-2fa/totp
```

## Request body

All fields are required.

```ts
{
    "code": string
}
```

- `code`: The TOTP code.


## Successful response

No response body (204).

## Error codes

- [400] `INVALID_DATA`: Invalid request data.
- [400] `SECOND_FACTOR_NOT_ALLOWED`: The user does not have a TOTP credential registered.
- [400] `TOO_MANY_REQUESTS`: Rate limit exceeded.
- [400] `INCORRECT_CODE`: Incorrect TOTP code.
- [404] `NOT_FOUND`: The reset request does not exist.
- [500] `UNKNOWN_ERROR`