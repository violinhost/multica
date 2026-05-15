# Multica user-binding / onboarding audit queries

Date: 2026-05-15
Host: us-dallas-svc-001
Author: Hermes
Purpose: reusable DB checks for auth/user-route anomalies.

## 1. Users missing external bindings
```sql
select email, onboarded_at
from "user"
where coalesce(external_provider, '') = ''
   or coalesce(external_user_id, '') = ''
order by email;
```

## 2. Users with memberships but not onboarded
```sql
select u.email,
       u.onboarded_at,
       count(m.*) as memberships
from "user" u
join member m on m.user_id = u.id
where u.onboarded_at is null
group by u.id, u.email, u.onboarded_at
order by u.email;
```

## 3. Users bound to Authentik
```sql
select email, external_user_id, onboarded_at
from "user"
where external_provider = 'authentik'
order by email;
```

## 4. Quick user-identity compare by email
Replace the emails before running.
```sql
select email,
       external_provider,
       external_user_id,
       onboarded_at,
       created_at,
       updated_at
from "user"
where email in (
  'violin.wang@galactic.holdings',
  'SOMEONE@galactic.holdings'
)
order by email;
```

## 5. Memberships for a specific user
Replace the email before running.
```sql
select u.email, w.name as workspace_name, m.role
from "user" u
join member m on m.user_id = u.id
join workspace w on w.id = m.workspace_id
where u.email = 'violin.wang@galactic.holdings'
order by w.name;
```

## 6. Interpretation notes
- Missing `external_provider/external_user_id` does not always break login if email fallback works.
- It does increase risk that OIDC login takes the "new user" branch if claims/email differ from the stored row.
- `onboarded_at is null` is enough to route a successfully logged-in user into `/onboarding` in the current code path.
- A user with memberships + `onboarded_at is null` is a high-value anomaly for Velafi self-host.
