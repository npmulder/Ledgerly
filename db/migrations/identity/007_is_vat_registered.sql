ALTER TABLE identity.company_profile
	ADD COLUMN is_vat_registered boolean NOT NULL DEFAULT false;

UPDATE identity.company_profile
SET is_vat_registered = true
WHERE vat_number IS NOT NULL
	AND btrim(vat_number) <> '';
