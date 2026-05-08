package store

import (
	"database/sql"
	"errors"

	"watcher/internal/model"
)

func (s *LocalStore) SaveHostFileRoot(root model.HostSavedFileRoot) (model.HostSavedFileRoot, error) {
	if root.RootID == "" {
		root.RootID = model.NewID("hostroot")
	}
	now := model.NowString()
	if root.CreatedAt == "" {
		root.CreatedAt = now
	}
	root.UpdatedAt = now
	_, err := s.db.Exec(
		`INSERT INTO host_file_roots (root_id, label, path, download, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(root_id) DO UPDATE SET
			 label = excluded.label,
			 path = excluded.path,
			 download = excluded.download,
			 updated_at = excluded.updated_at`,
		root.RootID,
		root.Label,
		root.Path,
		boolToInt(root.Download),
		root.CreatedAt,
		root.UpdatedAt,
	)
	return root, err
}

func (s *LocalStore) ListHostFileRoots() ([]model.HostSavedFileRoot, error) {
	rows, err := s.db.Query(
		`SELECT root_id, label, path, download, created_at, updated_at
		 FROM host_file_roots
		 ORDER BY updated_at DESC, root_id DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roots := []model.HostSavedFileRoot{}
	for rows.Next() {
		root, err := scanHostFileRoot(rows)
		if err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, rows.Err()
}

func (s *LocalStore) GetHostFileRoot(rootID string) (model.HostSavedFileRoot, error) {
	row := s.db.QueryRow(
		`SELECT root_id, label, path, download, created_at, updated_at
		 FROM host_file_roots
		 WHERE root_id = ?`,
		rootID,
	)
	root, err := scanHostFileRoot(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.HostSavedFileRoot{}, nil
		}
		return model.HostSavedFileRoot{}, err
	}
	return root, nil
}

func (s *LocalStore) DeleteHostFileRoot(rootID string) (bool, error) {
	result, err := s.db.Exec(`DELETE FROM host_file_roots WHERE root_id = ?`, rootID)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

func scanHostFileRoot(scanner interface{ Scan(dest ...any) error }) (model.HostSavedFileRoot, error) {
	var root model.HostSavedFileRoot
	var download int
	if err := scanner.Scan(
		&root.RootID,
		&root.Label,
		&root.Path,
		&download,
		&root.CreatedAt,
		&root.UpdatedAt,
	); err != nil {
		return model.HostSavedFileRoot{}, err
	}
	root.Download = download != 0
	return root, nil
}
