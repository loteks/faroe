---
title: "GET /users/[user_id]"
---

# GET /users/[user_id]

Gets a user.

```
GET https://your-domain.com/users/USER_ID
```

## Succesful response

Returns the [user model](/api-reference/rest/models/user) of the user if they exist.

## Error codess

- [404] `NOT_FOUND`: The user does not exist.
- [500] `UNKNOWN_ERROR`