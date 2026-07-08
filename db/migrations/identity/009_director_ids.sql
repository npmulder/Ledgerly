UPDATE identity.company_profile
SET directors = COALESCE((
	SELECT jsonb_agg(
		CASE
			WHEN NULLIF(btrim(director.value->>'id'), '') IS NOT NULL THEN director.value
			ELSE jsonb_set(director.value, '{id}', to_jsonb('director-' || director.ordinality::text), true)
		END
		ORDER BY director.ordinality
	)
	FROM jsonb_array_elements(directors) WITH ORDINALITY AS director(value, ordinality)
), '[]'::jsonb)
WHERE jsonb_typeof(directors) = 'array';
