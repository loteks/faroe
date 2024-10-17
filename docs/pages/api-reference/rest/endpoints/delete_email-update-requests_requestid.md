---
title: "DELETE /email-update-requests/[request_id]"
---

# DELETE /email-update-requests/[request_id]

Deletes an email update request.

```
DELETE https://your-domain.com/email-update-requests/[request_id]
```

## Succesful response

No response body (204).

## Error codess

- [404] `NOT_FOUND`: The request does not exist or has expired.
- [500] `UNKNOWN_ERROR`