package postgres

import (
	"context"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const backendColumns = `b.id, b.owner_id, b.name, b.ownership_status, b.operational_status,
	b.provider_auth_state, b.capabilities, b.max_concurrent_runs, b.active_runs,
	b.protocol_version, b.connection_id, b.last_heartbeat_at,
	b.created_at, b.updated_at, b.revoked_at`

// CreateBackendInstance inserts a BackendInstance, with the SHA-256 of the
// persistent credential it will present on every connection. The credential
// itself is never stored.
func (s *Store) CreateBackendInstance(ctx context.Context, b *domain.BackendInstance, credentialSHA256 string, claimCodeSHA256 *string, claimExpiry *time.Time) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO backend_instances (id, owner_id, name, ownership_status, operational_status,
		                               capabilities, max_concurrent_runs,
		                               credential_sha256, claim_code_sha256, claim_code_expires_at)
		VALUES ($1, $2, $3, $4, 'OFFLINE', $5, $6, $7, $8, $9)
		RETURNING created_at, updated_at`,
		b.ID, b.OwnerID, b.Name, b.OwnershipStatus, capabilityStrings(b.Capabilities),
		b.Capacity.MaxConcurrentRuns, credentialSHA256, claimCodeSHA256, claimExpiry,
	).Scan(&b.CreatedAt, &b.UpdatedAt)
	return classify(err, "create backend instance")
}

// BackendInstanceByCredential resolves the bearer credential a backend presents
// on Connect. A revoked instance never resolves again: a returning backend must
// register as a new one.
func (s *Store) BackendInstanceByCredential(ctx context.Context, credentialSHA256 string) (domain.BackendInstance, error) {
	return scanBackendInstance(s.q.QueryRow(ctx, `
		SELECT `+backendColumns+`
		FROM backend_instances b
		WHERE b.credential_sha256 = $1 AND b.ownership_status <> 'REVOKED'`, credentialSHA256))
}

// BackendInstanceByID returns a BackendInstance without an ownership check. It
// serves the control stream, where the caller is the instance itself.
func (s *Store) BackendInstanceByID(ctx context.Context, id domain.BackendInstanceID) (domain.BackendInstance, error) {
	return scanBackendInstance(s.q.QueryRow(ctx,
		`SELECT `+backendColumns+` FROM backend_instances b WHERE b.id = $1`, id))
}

// GetBackendInstance returns a BackendInstance owned by ownerID.
func (s *Store) GetBackendInstance(ctx context.Context, ownerID domain.UserID, id domain.BackendInstanceID) (domain.BackendInstance, error) {
	return scanBackendInstance(s.q.QueryRow(ctx, `
		SELECT `+backendColumns+` FROM backend_instances b WHERE b.id = $1 AND b.owner_id = $2`, id, ownerID))
}

// ListBackendInstances returns the BackendInstances of a user. They belong to
// the user, never to a Project.
func (s *Store) ListBackendInstances(ctx context.Context, ownerID domain.UserID) ([]domain.BackendInstance, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+backendColumns+`
		FROM backend_instances b
		WHERE b.owner_id = $1 AND b.ownership_status <> 'REVOKED'
		ORDER BY b.name`, ownerID)
	if err != nil {
		return nil, classify(err, "list backend instances")
	}
	defer rows.Close()

	var out []domain.BackendInstance
	for rows.Next() {
		instance, err := scanBackendInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, instance)
	}
	return out, classify(rows.Err(), "list backend instances")
}

// MarkBackendConnected records the connection lease and what the backend
// advertised in its Hello.
func (s *Store) MarkBackendConnected(ctx context.Context, id domain.BackendInstanceID, connectionID string, protocolVersion int, capabilities []domain.Capability, maxConcurrentRuns int, sdk, sdkVersion, backendName, backendVersion string) error {
	_, err := s.q.Exec(ctx, `
		UPDATE backend_instances
		SET connection_id = $2, connected_at = now(), last_heartbeat_at = now(),
		    operational_status = 'STARTING', protocol_version = $3, capabilities = $4,
		    max_concurrent_runs = $5, sdk_name = $6, sdk_version = $7,
		    backend_name = $8, backend_version = $9, updated_at = now()
		WHERE id = $1`,
		id, connectionID, protocolVersion, capabilityStrings(capabilities),
		maxConcurrentRuns, sdk, sdkVersion, backendName, backendVersion)
	return classify(err, "mark backend connected")
}

// MarkBackendDisconnected clears the lease and infers OFFLINE. A backend never
// reports OFFLINE itself.
func (s *Store) MarkBackendDisconnected(ctx context.Context, id domain.BackendInstanceID, connectionID string) error {
	_, err := s.q.Exec(ctx, `
		UPDATE backend_instances
		SET operational_status = 'OFFLINE', connection_id = NULL, connected_at = NULL, updated_at = now()
		WHERE id = $1 AND connection_id = $2`, id, connectionID)
	return classify(err, "mark backend disconnected")
}

// UpdateBackendStatus records a self-reported operational status and capacity.
func (s *Store) UpdateBackendStatus(ctx context.Context, id domain.BackendInstanceID, status domain.BackendOperationalStatus, providerAuth domain.ProviderAuthState, capacity domain.Capacity) error {
	_, err := s.q.Exec(ctx, `
		UPDATE backend_instances
		SET operational_status = $2,
		    provider_auth_state = COALESCE(NULLIF($3, ''), provider_auth_state),
		    max_concurrent_runs = $4, active_runs = $5, updated_at = now()
		WHERE id = $1`, id, status, string(providerAuth), capacity.MaxConcurrentRuns, capacity.ActiveRuns)
	return classify(err, "update backend status")
}

// RecordBackendHeartbeat refreshes the liveness timestamp.
func (s *Store) RecordBackendHeartbeat(ctx context.Context, id domain.BackendInstanceID, at time.Time, capacity domain.Capacity) error {
	_, err := s.q.Exec(ctx, `
		UPDATE backend_instances
		SET last_heartbeat_at = $2, active_runs = $3, updated_at = now()
		WHERE id = $1`, id, at, capacity.ActiveRuns)
	return classify(err, "record backend heartbeat")
}

// RevokeBackendInstance invalidates the persistent credential while keeping the
// record for historical references.
func (s *Store) RevokeBackendInstance(ctx context.Context, ownerID domain.UserID, id domain.BackendInstanceID) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE backend_instances
		SET ownership_status = 'REVOKED', operational_status = 'OFFLINE',
		    credential_sha256 = NULL, claim_code_sha256 = NULL,
		    connection_id = NULL, revoked_at = now(), updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND ownership_status <> 'REVOKED'`, id, ownerID)
	if err != nil {
		return classify(err, "revoke backend instance")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimBackendInstance associates an UNCLAIMED instance with a user, consuming
// its one-time claim code.
func (s *Store) ClaimBackendInstance(ctx context.Context, ownerID domain.UserID, claimCodeSHA256 string) (domain.BackendInstance, error) {
	return scanBackendInstance(s.q.QueryRow(ctx, `
		UPDATE backend_instances b
		SET owner_id = $1, ownership_status = 'CLAIMED',
		    claim_code_sha256 = NULL, claim_code_expires_at = NULL, updated_at = now()
		WHERE b.claim_code_sha256 = $2
		  AND b.ownership_status = 'UNCLAIMED'
		  AND b.claim_code_expires_at > now()
		RETURNING `+backendColumns, ownerID, claimCodeSHA256))
}

// CreateRegistrationToken stores a one-shot registration token, hashed.
func (s *Store) CreateRegistrationToken(ctx context.Context, id string, ownerID domain.UserID, tokenSHA256, label string, expiresAt time.Time) error {
	_, err := s.q.Exec(ctx, `
		INSERT INTO backend_registration_tokens (id, owner_id, token_sha256, label, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, id, ownerID, tokenSHA256, label, expiresAt)
	return classify(err, "create registration token")
}

// ConsumeRegistrationToken atomically marks a one-shot token used and returns
// its owner. A second attempt finds nothing: the token is single use.
func (s *Store) ConsumeRegistrationToken(ctx context.Context, tokenSHA256 string, instanceID domain.BackendInstanceID) (domain.UserID, error) {
	var ownerID domain.UserID
	err := s.q.QueryRow(ctx, `
		UPDATE backend_registration_tokens
		SET used_at = now(), backend_instance_id = $2
		WHERE token_sha256 = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING owner_id`, tokenSHA256, instanceID).Scan(&ownerID)
	return ownerID, classify(err, "consume registration token")
}

func capabilityStrings(capabilities []domain.Capability) []string {
	out := make([]string, 0, len(capabilities))
	for _, c := range capabilities {
		out = append(out, string(c))
	}
	return out
}

func scanBackendInstance(row scanner) (domain.BackendInstance, error) {
	var (
		instance     domain.BackendInstance
		capabilities []string
	)
	err := row.Scan(&instance.ID, &instance.OwnerID, &instance.Name, &instance.OwnershipStatus,
		&instance.OperationalStatus, &instance.ProviderAuthState, &capabilities,
		&instance.Capacity.MaxConcurrentRuns, &instance.Capacity.ActiveRuns,
		&instance.ProtocolVersion, &instance.ConnectionID, &instance.LastHeartbeatAt,
		&instance.CreatedAt, &instance.UpdatedAt, &instance.RevokedAt)
	if err != nil {
		return instance, classify(err, "read backend instance")
	}
	instance.Capabilities = make([]domain.Capability, 0, len(capabilities))
	for _, c := range capabilities {
		instance.Capabilities = append(instance.Capabilities, domain.Capability(c))
	}
	return instance, nil
}

// ReplaceBackendConditions records why a backend is in the state it reports.
//
// The report is complete, so a condition the backend no longer sends is gone:
// a resolved problem must stop being shown, which is the whole reason this is
// current state rather than a log.
func (s *Store) ReplaceBackendConditions(ctx context.Context, id domain.BackendInstanceID, conditions []domain.Condition) error {
	return s.WithTx(ctx, func(tx *Store) error {
		if _, err := tx.q.Exec(ctx,
			`DELETE FROM backend_conditions WHERE backend_instance_id = $1`, id); err != nil {
			return classify(err, "replace backend conditions")
		}
		for _, condition := range conditions {
			if _, err := tx.q.Exec(ctx, `
				INSERT INTO backend_conditions (backend_instance_id, type, status, reason, message)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (backend_instance_id, type) DO UPDATE SET
					status = EXCLUDED.status, reason = EXCLUDED.reason,
					message = EXCLUDED.message, observed_at = now()`,
				id, condition.Type, condition.Status, condition.Reason, condition.Message); err != nil {
				return classify(err, "replace backend conditions")
			}
		}
		return nil
	})
}

// BackendConditions returns the conditions of several BackendInstances at once,
// so a listing costs one query rather than one per row.
func (s *Store) BackendConditions(ctx context.Context, ids []domain.BackendInstanceID) (map[domain.BackendInstanceID][]domain.Condition, error) {
	out := make(map[domain.BackendInstanceID][]domain.Condition, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	rows, err := s.q.Query(ctx, `
		SELECT backend_instance_id, type, status, reason, message, observed_at
		FROM backend_conditions
		WHERE backend_instance_id = ANY($1)
		ORDER BY type`, ids)
	if err != nil {
		return nil, classify(err, "read backend conditions")
	}
	defer rows.Close()

	for rows.Next() {
		var id domain.BackendInstanceID
		var condition domain.Condition
		if err := rows.Scan(&id, &condition.Type, &condition.Status,
			&condition.Reason, &condition.Message, &condition.At); err != nil {
			return nil, classify(err, "read backend conditions")
		}
		out[id] = append(out[id], condition)
	}
	return out, classify(rows.Err(), "read backend conditions")
}
