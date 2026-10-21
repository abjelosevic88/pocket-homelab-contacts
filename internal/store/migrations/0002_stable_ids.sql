-- Contact ids are what notes and tasks link to (/contacts/<id>), so an id is
-- never handed to another contact, and a restored contact gets its own back.
ALTER TABLE deleted_contacts ADD COLUMN contact_id INTEGER NOT NULL DEFAULT 0;
INSERT OR IGNORE INTO settings (key, value) SELECT 'last_contact_id', COALESCE(MAX(id), 0) FROM contacts;
