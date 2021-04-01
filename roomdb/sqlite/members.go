package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/friendsofgo/errors"
	"github.com/mattn/go-sqlite3"
	"github.com/ssb-ngi-pointer/go-ssb-room/roomdb"
	"github.com/ssb-ngi-pointer/go-ssb-room/roomdb/sqlite/models"
	"github.com/volatiletech/sqlboiler/v4/boil"
	"github.com/volatiletech/sqlboiler/v4/queries/qm"
	refs "go.mindeco.de/ssb-refs"
)

// compiler assertion to ensure the struct fullfills the interface
var _ roomdb.MembersService = (*Members)(nil)

type Members struct {
	db *sql.DB
}

func (m Members) Add(ctx context.Context, pubKey refs.FeedRef, role roomdb.Role) (int64, error) {
	var newID int64
	err := transact(m.db, func(tx *sql.Tx) error {
		var err error
		newID, err = m.add(ctx, tx, pubKey, role)
		return err
	})
	if err != nil {
		return -1, err
	}
	return newID, nil
}

// no receiver name because it needs to use the passed transaction
func (Members) add(ctx context.Context, tx *sql.Tx, pubKey refs.FeedRef, role roomdb.Role) (int64, error) {
	if err := role.IsValid(); err != nil {
		return -1, err
	}

	if _, err := refs.ParseFeedRef(pubKey.Ref()); err != nil {
		return -1, err
	}

	var newMember models.Member
	newMember.PubKey = roomdb.DBFeedRef{FeedRef: pubKey}
	newMember.Role = int64(role)

	err := newMember.Insert(ctx, tx, boil.Infer())
	if err != nil {
		var sqlErr sqlite3.Error
		if errors.As(err, &sqlErr) && sqlErr.ExtendedCode == sqlite3.ErrConstraintUnique {
			return -1, roomdb.ErrAlreadyAdded{Ref: pubKey}
		}
		return -1, fmt.Errorf("members: failed to insert new user: %w", err)
	}

	return newMember.ID, nil
}

func (m Members) GetByID(ctx context.Context, mid int64) (roomdb.Member, error) {
	entry, err := models.FindMember(ctx, m.db, mid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return roomdb.Member{}, roomdb.ErrNotFound
		}
		return roomdb.Member{}, err
	}
	return roomdb.Member{
		ID:     entry.ID,
		Role:   roomdb.Role(entry.Role),
		PubKey: entry.PubKey.FeedRef,
	}, nil
}

// GetByFeed returns the member if it exists
func (m Members) GetByFeed(ctx context.Context, h refs.FeedRef) (roomdb.Member, error) {
	entry, err := models.Members(qm.Where("pub_key = ?", h.Ref())).One(ctx, m.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return roomdb.Member{}, roomdb.ErrNotFound
		}
		return roomdb.Member{}, err
	}
	return roomdb.Member{
		ID:     entry.ID,
		Role:   roomdb.Role(entry.Role),
		PubKey: entry.PubKey.FeedRef,
	}, nil
}

// List returns a list of all the feeds.
func (m Members) List(ctx context.Context) ([]roomdb.Member, error) {
	all, err := models.Members().All(ctx, m.db)
	if err != nil {
		return nil, err
	}

	var members = make([]roomdb.Member, len(all))
	for i, listEntry := range all {
		members[i].ID = listEntry.ID
		members[i].Role = roomdb.Role(listEntry.Role)
		members[i].PubKey = listEntry.PubKey.FeedRef
	}

	return members, nil
}

func (m Members) Count(ctx context.Context) (uint, error) {
	count, err := models.Members().Count(ctx, m.db)
	if err != nil {
		return 0, err
	}
	return uint(count), nil
}

// RemoveFeed removes the feed from the list.
func (m Members) RemoveFeed(ctx context.Context, r refs.FeedRef) error {
	entry, err := models.Members(qm.Where("pub_key = ?", r.Ref())).One(ctx, m.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return roomdb.ErrNotFound
		}
		return err
	}

	_, err = entry.Delete(ctx, m.db)
	if err != nil {
		return err
	}

	return nil
}

// RemoveID removes the feed from the list.
func (m Members) RemoveID(ctx context.Context, id int64) error {
	entry, err := models.FindMember(ctx, m.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return roomdb.ErrNotFound
		}
		return err
	}

	_, err = entry.Delete(ctx, m.db)
	if err != nil {
		return err
	}

	return nil
}

// SetRole updates the role r of the passed memberID.
func (m Members) SetRole(ctx context.Context, id int64, r roomdb.Role) error {
	if err := r.IsValid(); err != nil {
		return err
	}

	return transact(m.db, func(tx *sql.Tx) error {
		m, err := models.FindMember(ctx, tx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return roomdb.ErrNotFound
			}
			return err
		}

		// find the number of other admins
		admins, err := models.Members(
			qm.Where("id != ?", id),
			qm.Where("role = ?", roomdb.RoleAdmin),
		).Count(ctx, tx)
		if err != nil {
			return err
		}

		if admins < 1 {
			return fmt.Errorf("need at least one other admin")
		}

		m.Role = int64(r)
		_, err = m.Update(ctx, tx, boil.Whitelist("role"))
		return err
	})
}
