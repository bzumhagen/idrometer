package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

// Timestamps are stored as unix milliseconds; 0 / NULL map to the zero time / nil.

func toMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func toNullMS(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

func fromNullMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func nullFloat(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

// ---- devices ----

type devices struct{ q queries }

const deviceCols = `id, source, hardware_id, reported_name, display_name, gap_threshold_s,
	gravity_unit, last_seen_at, last_battery_v, last_rssi, created_at`

func scanDevice(row interface{ Scan(...any) error }) (domain.Device, error) {
	var (
		d             domain.Device
		gap           sql.NullInt64
		seen, created int64
		unit          string
	)
	err := row.Scan(&d.ID, &d.Source, &d.HardwareID, &d.ReportedName, &d.DisplayName, &gap,
		&unit, &seen, &d.LastBatteryV, &d.LastRSSI, &created)
	if err != nil {
		return d, mapErr(err)
	}
	if gap.Valid {
		g := time.Duration(gap.Int64) * time.Second
		d.GapThreshold = &g
	}
	d.GravityUnit = domain.GravityUnit(unit)
	d.LastSeenAt, d.CreatedAt = fromMS(seen), fromMS(created)
	return d, nil
}

func gapSeconds(d domain.Device) sql.NullInt64 {
	if d.GapThreshold == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(d.GapThreshold.Seconds()), Valid: true}
}

func (r devices) Get(ctx context.Context, id int64) (domain.Device, error) {
	return scanDevice(r.q.db.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = ?`, id))
}

func (r devices) GetBySource(ctx context.Context, source, hw string) (domain.Device, error) {
	return scanDevice(r.q.db.QueryRowContext(ctx,
		`SELECT `+deviceCols+` FROM devices WHERE source = ? AND hardware_id = ?`, source, hw))
}

func (r devices) Create(ctx context.Context, d *domain.Device) error {
	unit := d.GravityUnit
	if unit == "" {
		unit = domain.GravityAuto
	}
	res, err := r.q.db.ExecContext(ctx, `INSERT INTO devices
		(source, hardware_id, reported_name, display_name, gap_threshold_s, gravity_unit,
		 last_seen_at, last_battery_v, last_rssi, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		d.Source, d.HardwareID, d.ReportedName, d.DisplayName, gapSeconds(*d), string(unit),
		toMS(d.LastSeenAt), d.LastBatteryV, d.LastRSSI, toMS(d.CreatedAt))
	if err != nil {
		return mapErr(err)
	}
	d.ID, err = res.LastInsertId()
	d.GravityUnit = unit
	return err
}

func (r devices) Update(ctx context.Context, d domain.Device) error {
	res, err := r.q.db.ExecContext(ctx, `UPDATE devices SET reported_name=?, display_name=?,
		gap_threshold_s=?, gravity_unit=?, last_seen_at=?, last_battery_v=?, last_rssi=?
		WHERE id=?`,
		d.ReportedName, d.DisplayName, gapSeconds(d), string(d.GravityUnit),
		toMS(d.LastSeenAt), d.LastBatteryV, d.LastRSSI, d.ID)
	return affectedOne(res, err)
}

func (r devices) List(ctx context.Context) ([]domain.Device, error) {
	rows, err := r.q.db.QueryContext(ctx, `SELECT `+deviceCols+` FROM devices ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return mapErr(err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ---- fermentations ----

type fermentations struct{ q queries }

const fermCols = `id, device_id, recipe_id, name, status, started_at, ended_at,
	close_reason, og_override, notes, deleted_at`

func scanFerm(row interface{ Scan(...any) error }) (domain.Fermentation, error) {
	var (
		f              domain.Fermentation
		recipe         sql.NullInt64
		og             sql.NullFloat64
		started        int64
		ended, delAt   sql.NullInt64
		status, reason string
	)
	err := row.Scan(&f.ID, &f.DeviceID, &recipe, &f.Name, &status, &started, &ended,
		&reason, &og, &f.Notes, &delAt)
	if err != nil {
		return f, mapErr(err)
	}
	if recipe.Valid {
		f.RecipeID = &recipe.Int64
	}
	if og.Valid {
		f.OGOverride = &og.Float64
	}
	f.Status, f.CloseReason = domain.FermentationStatus(status), domain.CloseReason(reason)
	f.StartedAt, f.EndedAt, f.DeletedAt = fromMS(started), fromNullMS(ended), fromNullMS(delAt)
	return f, nil
}

func (r fermentations) Get(ctx context.Context, id int64) (domain.Fermentation, error) {
	return scanFerm(r.q.db.QueryRowContext(ctx, `SELECT `+fermCols+` FROM fermentations WHERE id = ?`, id))
}

func (r fermentations) Create(ctx context.Context, f *domain.Fermentation) error {
	res, err := r.q.db.ExecContext(ctx, `INSERT INTO fermentations
		(device_id, recipe_id, name, status, started_at, ended_at, close_reason, og_override, notes, deleted_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		f.DeviceID, nullInt(f.RecipeID), f.Name, string(f.Status), toMS(f.StartedAt), toNullMS(f.EndedAt),
		string(f.CloseReason), nullFloat(f.OGOverride), f.Notes, toNullMS(f.DeletedAt))
	if err != nil {
		return mapErr(err)
	}
	f.ID, err = res.LastInsertId()
	return err
}

func (r fermentations) Update(ctx context.Context, f domain.Fermentation) error {
	res, err := r.q.db.ExecContext(ctx, `UPDATE fermentations SET recipe_id=?, name=?, status=?,
		started_at=?, ended_at=?, close_reason=?, og_override=?, notes=?, deleted_at=? WHERE id=?`,
		nullInt(f.RecipeID), f.Name, string(f.Status), toMS(f.StartedAt), toNullMS(f.EndedAt),
		string(f.CloseReason), nullFloat(f.OGOverride), f.Notes, toNullMS(f.DeletedAt), f.ID)
	return affectedOne(res, err)
}

func (r fermentations) ActiveForDevice(ctx context.Context, deviceID int64) (domain.Fermentation, error) {
	return scanFerm(r.q.db.QueryRowContext(ctx, `SELECT `+fermCols+` FROM fermentations
		WHERE device_id = ? AND status = 'active' AND deleted_at IS NULL`, deviceID))
}

func (r fermentations) ListActive(ctx context.Context) ([]domain.Fermentation, error) {
	return r.List(ctx, domain.FermentationFilter{Status: domain.StatusActive})
}

func (r fermentations) List(ctx context.Context, fl domain.FermentationFilter) ([]domain.Fermentation, error) {
	q := `SELECT ` + fermCols + ` FROM fermentations WHERE 1=1`
	var args []any
	switch {
	case fl.OnlyDeleted:
		q += ` AND deleted_at IS NOT NULL`
	case !fl.IncludeDeleted:
		q += ` AND deleted_at IS NULL`
	}
	if fl.Status != "" {
		q += ` AND status = ?`
		args = append(args, string(fl.Status))
	}
	if fl.DeviceID != 0 {
		q += ` AND device_id = ?`
		args = append(args, fl.DeviceID)
	}
	rows, err := r.q.db.QueryContext(ctx, q+` ORDER BY started_at DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Fermentation
	for rows.Next() {
		f, err := scanFerm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---- readings ----

type readings struct{ q queries }

func (r readings) Insert(ctx context.Context, v *domain.Reading) error {
	res, err := r.q.db.ExecContext(ctx, `INSERT INTO readings
		(device_id, fermentation_id, received_at, angle, temperature_c, battery_v,
		 gravity_device, rssi, interval_s, excluded, raw_payload)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		v.DeviceID, v.FermentationID, toMS(v.ReceivedAt), v.Angle, v.TemperatureC, v.BatteryV,
		v.GravityDevice, v.RSSI, v.IntervalS, v.Excluded, v.RawPayload)
	if err != nil {
		return mapErr(err)
	}
	v.ID, err = res.LastInsertId()
	return err
}

func (r readings) Bounds(ctx context.Context, id int64) (first, last time.Time, ok bool, err error) {
	var lo, hi sql.NullInt64
	err = r.q.db.QueryRowContext(ctx,
		`SELECT MIN(received_at), MAX(received_at) FROM readings WHERE fermentation_id = ?`, id).Scan(&lo, &hi)
	if err != nil || !lo.Valid {
		return time.Time{}, time.Time{}, false, err
	}
	return fromMS(lo.Int64), fromMS(hi.Int64), true, nil
}

func (r readings) List(ctx context.Context, id int64, includeExcluded bool) ([]domain.Reading, error) {
	q := `SELECT id, device_id, fermentation_id, received_at, angle, temperature_c, battery_v,
		gravity_device, rssi, interval_s, excluded, raw_payload
		FROM readings WHERE fermentation_id = ?`
	if !includeExcluded {
		q += ` AND excluded = 0`
	}
	rows, err := r.q.db.QueryContext(ctx, q+` ORDER BY received_at, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Reading
	for rows.Next() {
		var v domain.Reading
		var at int64
		if err := rows.Scan(&v.ID, &v.DeviceID, &v.FermentationID, &at, &v.Angle, &v.TemperatureC,
			&v.BatteryV, &v.GravityDevice, &v.RSSI, &v.IntervalS, &v.Excluded, &v.RawPayload); err != nil {
			return nil, err
		}
		v.ReceivedAt = fromMS(at)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r readings) MoveSince(ctx context.Context, from, to int64, since time.Time) error {
	_, err := r.q.db.ExecContext(ctx,
		`UPDATE readings SET fermentation_id = ? WHERE fermentation_id = ? AND received_at >= ?`,
		to, from, toMS(since))
	return mapErr(err)
}

func (r readings) MoveAll(ctx context.Context, from, to int64) error {
	_, err := r.q.db.ExecContext(ctx,
		`UPDATE readings SET fermentation_id = ? WHERE fermentation_id = ?`, to, from)
	return mapErr(err)
}

func (r readings) SetExcluded(ctx context.Context, id int64, from, to time.Time, ex bool) (int64, error) {
	res, err := r.q.db.ExecContext(ctx, `UPDATE readings SET excluded = ?
		WHERE fermentation_id = ? AND received_at BETWEEN ? AND ? AND excluded <> ?`,
		ex, id, toMS(from), toMS(to), ex)
	if err != nil {
		return 0, mapErr(err)
	}
	return res.RowsAffected()
}

// ---- settings ----

type settings struct{ q queries }

func (r settings) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := r.q.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v, mapErr(err)
}

func (r settings) Set(ctx context.Context, key, value string) error {
	_, err := r.q.db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return mapErr(err)
}

func (r readings) Latest(ctx context.Context, id int64) (domain.Reading, error) {
	var v domain.Reading
	var at int64
	err := r.q.db.QueryRowContext(ctx, `SELECT id, device_id, fermentation_id, received_at, angle,
		temperature_c, battery_v, gravity_device, rssi, interval_s, excluded, raw_payload
		FROM readings WHERE fermentation_id = ? ORDER BY received_at DESC, id DESC LIMIT 1`, id).
		Scan(&v.ID, &v.DeviceID, &v.FermentationID, &at, &v.Angle, &v.TemperatureC,
			&v.BatteryV, &v.GravityDevice, &v.RSSI, &v.IntervalS, &v.Excluded, &v.RawPayload)
	if err != nil {
		return v, mapErr(err)
	}
	v.ReceivedAt = fromMS(at)
	return v, nil
}

// ---- users & sessions ----

type users struct{ q queries }

func (r users) Get(ctx context.Context, name string) (domain.User, error) {
	var u domain.User
	var created int64
	err := r.q.db.QueryRowContext(ctx,
		`SELECT username, password_hash, created_at FROM users WHERE username = ?`, name).
		Scan(&u.Username, &u.PasswordHash, &created)
	u.CreatedAt = fromMS(created)
	return u, mapErr(err)
}

func (r users) Upsert(ctx context.Context, u domain.User) error {
	_, err := r.q.db.ExecContext(ctx, `INSERT INTO users(username, password_hash, created_at) VALUES(?,?,?)
		ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash`,
		u.Username, u.PasswordHash, toMS(u.CreatedAt))
	return mapErr(err)
}

type sessions struct{ q queries }

func (r sessions) Create(ctx context.Context, s domain.Session) error {
	_, err := r.q.db.ExecContext(ctx, `INSERT INTO sessions(token_hash, username, created_at, expires_at)
		VALUES(?,?,?,?)`, s.TokenHash, s.Username, toMS(s.CreatedAt), toMS(s.ExpiresAt))
	return mapErr(err)
}

func (r sessions) Get(ctx context.Context, h string) (domain.Session, error) {
	var s domain.Session
	var created, expires int64
	err := r.q.db.QueryRowContext(ctx,
		`SELECT token_hash, username, created_at, expires_at FROM sessions WHERE token_hash = ?`, h).
		Scan(&s.TokenHash, &s.Username, &created, &expires)
	s.CreatedAt, s.ExpiresAt = fromMS(created), fromMS(expires)
	return s, mapErr(err)
}

func (r sessions) Delete(ctx context.Context, h string) error {
	_, err := r.q.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, h)
	return mapErr(err)
}

func (r sessions) DeleteExpired(ctx context.Context, now time.Time) error {
	_, err := r.q.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, toMS(now))
	return mapErr(err)
}

func (r sessions) DeleteForUser(ctx context.Context, name string) error {
	_, err := r.q.db.ExecContext(ctx, `DELETE FROM sessions WHERE username = ?`, name)
	return mapErr(err)
}

// ---- recipes ----

type recipes struct{ q queries }

const recipeCols = `id, name, style, target_og, target_fg, yeast, notes, created_at, updated_at`

func scanRecipe(row interface{ Scan(...any) error }) (domain.Recipe, error) {
	var r domain.Recipe
	var og, fg sql.NullFloat64
	var created, updated int64
	if err := row.Scan(&r.ID, &r.Name, &r.Style, &og, &fg, &r.Yeast, &r.Notes, &created, &updated); err != nil {
		return r, mapErr(err)
	}
	if og.Valid {
		r.TargetOG = &og.Float64
	}
	if fg.Valid {
		r.TargetFG = &fg.Float64
	}
	r.CreatedAt, r.UpdatedAt = fromMS(created), fromMS(updated)
	return r, nil
}

func (r recipes) Get(ctx context.Context, id int64) (domain.Recipe, error) {
	return scanRecipe(r.q.db.QueryRowContext(ctx, `SELECT `+recipeCols+` FROM recipes WHERE id = ?`, id))
}

func (r recipes) Create(ctx context.Context, v *domain.Recipe) error {
	res, err := r.q.db.ExecContext(ctx, `INSERT INTO recipes
		(name, style, target_og, target_fg, yeast, notes, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		v.Name, v.Style, nullFloat(v.TargetOG), nullFloat(v.TargetFG), v.Yeast, v.Notes,
		toMS(v.CreatedAt), toMS(v.UpdatedAt))
	if err != nil {
		return mapErr(err)
	}
	v.ID, err = res.LastInsertId()
	return err
}

func (r recipes) Update(ctx context.Context, v domain.Recipe) error {
	res, err := r.q.db.ExecContext(ctx, `UPDATE recipes SET name=?, style=?, target_og=?, target_fg=?,
		yeast=?, notes=?, updated_at=? WHERE id=?`,
		v.Name, v.Style, nullFloat(v.TargetOG), nullFloat(v.TargetFG), v.Yeast, v.Notes, toMS(v.UpdatedAt), v.ID)
	return affectedOne(res, err)
}

func (r recipes) Delete(ctx context.Context, id int64) error {
	res, err := r.q.db.ExecContext(ctx, `DELETE FROM recipes WHERE id = ?`, id) // FK sets fermentations.recipe_id NULL
	return affectedOne(res, err)
}

func (r recipes) List(ctx context.Context) ([]domain.Recipe, error) {
	rows, err := r.q.db.QueryContext(ctx, `SELECT `+recipeCols+` FROM recipes ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Recipe
	for rows.Next() {
		v, err := scanRecipe(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- manual readings ----

type manualReadings struct{ q queries }

const manualCols = `id, fermentation_id, taken_at, gravity_sg, temperature_c, method, is_og, is_fg, note`

func scanManual(row interface{ Scan(...any) error }) (domain.ManualReading, error) {
	var m domain.ManualReading
	var at int64
	var temp sql.NullFloat64
	var method string
	if err := row.Scan(&m.ID, &m.FermentationID, &at, &m.GravitySG, &temp, &method, &m.IsOG, &m.IsFG, &m.Note); err != nil {
		return m, mapErr(err)
	}
	m.TakenAt, m.Method = fromMS(at), domain.MeasureMethod(method)
	if temp.Valid {
		m.TemperatureC = &temp.Float64
	}
	return m, nil
}

func (r manualReadings) Get(ctx context.Context, id int64) (domain.ManualReading, error) {
	return scanManual(r.q.db.QueryRowContext(ctx, `SELECT `+manualCols+` FROM manual_readings WHERE id = ?`, id))
}

func (r manualReadings) Create(ctx context.Context, m *domain.ManualReading) error {
	res, err := r.q.db.ExecContext(ctx, `INSERT INTO manual_readings
		(fermentation_id, taken_at, gravity_sg, temperature_c, method, is_og, is_fg, note) VALUES (?,?,?,?,?,?,?,?)`,
		m.FermentationID, toMS(m.TakenAt), m.GravitySG, nullFloat(m.TemperatureC), string(m.Method), m.IsOG, m.IsFG, m.Note)
	if err != nil {
		return mapErr(err)
	}
	m.ID, err = res.LastInsertId()
	return err
}

func (r manualReadings) Update(ctx context.Context, m domain.ManualReading) error {
	res, err := r.q.db.ExecContext(ctx, `UPDATE manual_readings SET taken_at=?, gravity_sg=?, temperature_c=?,
		method=?, is_og=?, is_fg=?, note=? WHERE id=?`,
		toMS(m.TakenAt), m.GravitySG, nullFloat(m.TemperatureC), string(m.Method), m.IsOG, m.IsFG, m.Note, m.ID)
	return affectedOne(res, err)
}

func (r manualReadings) Delete(ctx context.Context, id int64) error {
	res, err := r.q.db.ExecContext(ctx, `DELETE FROM manual_readings WHERE id = ?`, id)
	return affectedOne(res, err)
}

func (r manualReadings) List(ctx context.Context, fid int64) ([]domain.ManualReading, error) {
	rows, err := r.q.db.QueryContext(ctx,
		`SELECT `+manualCols+` FROM manual_readings WHERE fermentation_id = ? ORDER BY taken_at, id`, fid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ManualReading
	for rows.Next() {
		m, err := scanManual(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r manualReadings) MoveSince(ctx context.Context, from, to int64, since time.Time) error {
	_, err := r.q.db.ExecContext(ctx,
		`UPDATE manual_readings SET fermentation_id = ? WHERE fermentation_id = ? AND taken_at >= ?`, to, from, toMS(since))
	return mapErr(err)
}

func (r manualReadings) MoveAll(ctx context.Context, from, to int64) error {
	_, err := r.q.db.ExecContext(ctx, `UPDATE manual_readings SET fermentation_id = ? WHERE fermentation_id = ?`, to, from)
	return mapErr(err)
}

// ---- annotations ----

type annotations struct{ q queries }

func (r annotations) Get(ctx context.Context, id int64) (domain.Annotation, error) {
	var a domain.Annotation
	var at int64
	var kind string
	err := r.q.db.QueryRowContext(ctx, `SELECT id, fermentation_id, at, kind, note FROM annotations WHERE id = ?`, id).
		Scan(&a.ID, &a.FermentationID, &at, &kind, &a.Note)
	a.At, a.Kind = fromMS(at), kind
	return a, mapErr(err)
}

func (r annotations) Create(ctx context.Context, a *domain.Annotation) error {
	res, err := r.q.db.ExecContext(ctx,
		`INSERT INTO annotations(fermentation_id, at, kind, note) VALUES (?,?,?,?)`,
		a.FermentationID, toMS(a.At), a.Kind, a.Note)
	if err != nil {
		return mapErr(err)
	}
	a.ID, err = res.LastInsertId()
	return err
}

func (r annotations) Delete(ctx context.Context, id int64) error {
	res, err := r.q.db.ExecContext(ctx, `DELETE FROM annotations WHERE id = ?`, id)
	return affectedOne(res, err)
}

func (r annotations) List(ctx context.Context, fid int64) ([]domain.Annotation, error) {
	rows, err := r.q.db.QueryContext(ctx,
		`SELECT id, fermentation_id, at, kind, note FROM annotations WHERE fermentation_id = ? ORDER BY at, id`, fid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Annotation
	for rows.Next() {
		var a domain.Annotation
		var at int64
		var kind string
		if err := rows.Scan(&a.ID, &a.FermentationID, &at, &kind, &a.Note); err != nil {
			return nil, err
		}
		a.At, a.Kind = fromMS(at), kind
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r annotations) MoveSince(ctx context.Context, from, to int64, since time.Time) error {
	_, err := r.q.db.ExecContext(ctx,
		`UPDATE annotations SET fermentation_id = ? WHERE fermentation_id = ? AND at >= ?`, to, from, toMS(since))
	return mapErr(err)
}

func (r annotations) MoveAll(ctx context.Context, from, to int64) error {
	_, err := r.q.db.ExecContext(ctx, `UPDATE annotations SET fermentation_id = ? WHERE fermentation_id = ?`, to, from)
	return mapErr(err)
}

// ---- calibrations ----

type calibrations struct{ q queries }

func (r calibrations) Get(ctx context.Context, deviceID int64) (domain.Calibration, error) {
	var c domain.Calibration
	var unit string
	var updated int64
	err := r.q.db.QueryRowContext(ctx, `SELECT device_id, c0, c1, c2, c3, output_unit, note, updated_at
		FROM device_calibrations WHERE device_id = ?`, deviceID).
		Scan(&c.DeviceID, &c.C[0], &c.C[1], &c.C[2], &c.C[3], &unit, &c.Note, &updated)
	c.OutputUnit, c.UpdatedAt = domain.GravityUnit(unit), fromMS(updated)
	return c, mapErr(err)
}

func (r calibrations) Set(ctx context.Context, c domain.Calibration) error {
	_, err := r.q.db.ExecContext(ctx, `INSERT INTO device_calibrations
		(device_id, c0, c1, c2, c3, output_unit, note, updated_at) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(device_id) DO UPDATE SET c0=excluded.c0, c1=excluded.c1, c2=excluded.c2, c3=excluded.c3,
		output_unit=excluded.output_unit, note=excluded.note, updated_at=excluded.updated_at`,
		c.DeviceID, c.C[0], c.C[1], c.C[2], c.C[3], string(c.OutputUnit), c.Note, toMS(c.UpdatedAt))
	return mapErr(err)
}

func (r calibrations) Delete(ctx context.Context, deviceID int64) error {
	_, err := r.q.db.ExecContext(ctx, `DELETE FROM device_calibrations WHERE device_id = ?`, deviceID)
	return mapErr(err)
}

// ---- event types ----

type annotationTypes struct{ q queries }

func (r annotationTypes) List(ctx context.Context) ([]domain.AnnotationType, error) {
	rows, err := r.q.db.QueryContext(ctx, `SELECT id, name FROM annotation_types ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AnnotationType
	for rows.Next() {
		var t domain.AnnotationType
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r annotationTypes) Create(ctx context.Context, t *domain.AnnotationType) error {
	res, err := r.q.db.ExecContext(ctx, `INSERT INTO annotation_types(name) VALUES (?)`, t.Name)
	if err != nil {
		return mapErr(err)
	}
	t.ID, err = res.LastInsertId()
	return err
}

func (r annotationTypes) Rename(ctx context.Context, id int64, name string) error {
	var old string
	if err := r.q.db.QueryRowContext(ctx, `SELECT name FROM annotation_types WHERE id = ?`, id).Scan(&old); err != nil {
		return mapErr(err)
	}
	if _, err := r.q.db.ExecContext(ctx, `UPDATE annotation_types SET name = ? WHERE id = ?`, name, id); err != nil {
		return mapErr(err)
	}
	_, err := r.q.db.ExecContext(ctx, `UPDATE annotations SET kind = ? WHERE kind = ?`, name, old)
	return mapErr(err)
}

func (r annotationTypes) Delete(ctx context.Context, id int64) error {
	res, err := r.q.db.ExecContext(ctx, `DELETE FROM annotation_types WHERE id = ?`, id)
	return affectedOne(res, err)
}
