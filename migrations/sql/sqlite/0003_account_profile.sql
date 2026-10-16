-- The Google account a Drive connection belongs to.
--
-- The console shows who is signed in. Until now it could only show the configured
-- tenant and user ids, which are deployment labels rather than a person: an operator
-- looking at "user_dev_admin @ tenant_dev_admin" has no way to tell which of their
-- accounts is connected.
--
-- Google already returns all of this in the `about` call the connect flow makes to
-- learn the subject. It was being fetched and discarded, so these columns record what
-- was already on the wire rather than adding a request.
--
-- display_name is preferred over email_address because a person recognises a name
-- faster than an address, and picture_url is optional: it is absent for accounts with
-- no photo, and the console falls back to an initial in that case.
--
-- None of this is a credential. An email address and a display name are not secrets,
-- and the picture URL is a public CDN link, not a bearer token.

ALTER TABLE drive_connections ADD COLUMN account_email   TEXT;
ALTER TABLE drive_connections ADD COLUMN account_name    TEXT;
ALTER TABLE drive_connections ADD COLUMN account_picture TEXT;
