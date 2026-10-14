package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"strconv"
	"strings"
)

func ingestOutgoing(ctx context.Context, tx *sql.Tx, e protocol.Event, eventHash string, now int64) error {
	if e.Type == 503 {
		refs, err := e.References()
		if err != nil {
			return err
		}
		if len(refs) != 1 || e.SMSStatus == nil {
			return nil
		}
		report := int64(e.DeliveryTS)
		if report == 0 {
			report = int64(e.MessageTS)
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO delivery_receipts(event_hash,device_id,slot,phone,iccid,mr,status,scts,report_ts) VALUES(?,?,?,?,?,?,?,?,?)`, eventHash, e.DeviceID, e.Slot, e.Phone, e.ICCID, refs[0], *e.SMSStatus, e.SCTS, report)
		if err != nil {
			return err
		}
		return rematchReceipts(ctx, tx, e.DeviceID, int(e.Slot), e.Phone)
	}
	if e.Type != 502 && !((e.Type == 401 || e.Type == 402) && e.SubType == 16) {
		return nil
	}
	if e.TID == "" {
		return nil
	}
	o, err := scanOutgoing(tx.QueryRowContext(ctx, `SELECT `+outgoingColumns+` FROM outgoing WHERE device_id=? AND tid=? AND dispatched_at>0`, e.DeviceID, e.TID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.Type == 502 && (int(e.Slot) != o.Slot || e.Phone != o.Phone || (e.Body != "" && e.Body != o.Body) || (e.ICCID != "" && e.ICCID != o.ICCID)) {
		return nil
	}
	if e.Type == 502 {
		_, err = tx.ExecContext(ctx, `UPDATE outgoing SET state='submitted',rank=4,updated_at=?,error_code=CASE WHEN error_code='reference_conflict' THEN error_code ELSE '' END WHERE id=?`, now, o.ID)
	} else if e.Code != nil {
		if *e.Code != 0 && o.Rank < 4 && (e.Type == 402 || o.Rank <= 2) {
			state, code := "failed", "device_rejected"
			if e.Type == 402 && (e.Val == "timeout" || e.Val == "unknow" || e.Val == "cancel") {
				state, code = "unknown", "device_result_unknown"
			}
			_, err = tx.ExecContext(ctx, `UPDATE outgoing SET state=?,error_code=CASE WHEN error_code='reference_conflict' THEN error_code ELSE ? END,updated_at=? WHERE id=? AND rank<4`, state, code, now, o.ID)
		} else if *e.Code == 0 {
			rank, state := 2, "accepted"
			if e.Type == 402 {
				rank, state = 3, "processed"
			}
			_, err = tx.ExecContext(ctx, `UPDATE outgoing SET rank=?,state=CASE WHEN error_code='device_result_unknown' THEN 'unknown' ELSE ? END,updated_at=? WHERE id=? AND rank<? AND state!='failed'`, rank, state, now, o.ID, rank)
		}
	}
	if err != nil {
		return err
	}
	var refs []int
	if e.Type == 502 {
		refs, err = e.References()
		if err != nil {
			return err
		}
	} else if e.Type == 402 && e.Code != nil && *e.Code == 0 && e.Val != "" {
		seen := map[int]bool{}
		for _, v := range strings.Split(e.Val, ",") {
			n, parseErr := strconv.Atoi(strings.TrimSpace(v))
			if parseErr != nil || n < 0 || n > 255 || seen[n] {
				refs = nil
				break
			}
			seen[n] = true
			refs = append(refs, n)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT mr FROM outgoing_refs WHERE operation_id=?`, o.ID)
	if err != nil {
		return err
	}
	old := map[int]bool{}
	for rows.Next() {
		var n int
		if err = rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		old[n] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	conflict := len(old) > 0 && len(old) != len(refs)
	for _, n := range refs {
		if len(old) > 0 && !old[n] {
			conflict = true
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO outgoing_refs VALUES(?,?)`, o.ID, n); err != nil {
			return err
		}
	}
	if conflict {
		if _, err = tx.ExecContext(ctx, `UPDATE outgoing SET error_code='reference_conflict' WHERE id=?`, o.ID); err != nil {
			return err
		}
	}
	return rematchReceipts(ctx, tx, o.DeviceID, o.Slot, o.Phone)
}

// Re-evaluate all reports, including earlier matches: a later reused MR may
// introduce ambiguity. Never pick the newest operation merely by arrival order.
func rematchReceipts(ctx context.Context, tx *sql.Tx, device string, slot int, phone string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,iccid,mr,scts FROM delivery_receipts WHERE device_id=? AND slot=? AND phone=?`, device, slot, phone)
	if err != nil {
		return err
	}
	type receipt struct {
		id, scts int64
		icc      string
		mr       int
	}
	list := []receipt{}
	for rows.Next() {
		var r receipt
		if err = rows.Scan(&r.id, &r.icc, &r.mr, &r.scts); err != nil {
			rows.Close()
			return err
		}
		list = append(list, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range list {
		var match any
		state := "unmatched"
		if r.scts > 0 {
			candidates, e := tx.QueryContext(ctx, `SELECT o.id FROM outgoing o JOIN outgoing_refs f ON f.operation_id=o.id WHERE o.device_id=? AND o.slot=? AND o.phone=? AND f.mr=? AND o.dispatched_at>0 AND abs(o.dispatched_at-?)<=300 AND (?='' OR o.iccid=?)`, device, slot, phone, r.mr, r.scts, r.icc, r.icc)
			if e != nil {
				return e
			}
			ids := []string{}
			for candidates.Next() {
				var id string
				if e = candidates.Scan(&id); e != nil {
					candidates.Close()
					return e
				}
				ids = append(ids, id)
			}
			e = candidates.Err()
			candidates.Close()
			if e != nil {
				return e
			}
			if len(ids) == 1 {
				match, state = ids[0], "matched"
			} else if len(ids) > 1 {
				state = "ambiguous"
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE delivery_receipts SET matched_operation=?,match_state=? WHERE id=?`, match, state, r.id); err != nil {
			return err
		}
	}
	return nil
}
func deliveryView(ctx context.Context, tx *sql.Tx, o Outgoing) (Outgoing, error) {
	rows, e := tx.QueryContext(ctx, `SELECT mr FROM outgoing_refs WHERE operation_id=?`, o.ID)
	if e != nil {
		return o, e
	}
	refs := []int{}
	for rows.Next() {
		var n int
		if e = rows.Scan(&n); e != nil {
			rows.Close()
			return o, e
		}
		refs = append(refs, n)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return o, e
	}
	o.DeliveryExpected = len(refs)
	if o.Rank != 4 || len(refs) == 0 || o.ErrorCode == "reference_conflict" {
		return o, nil
	}
	success, failed, temporary, ambiguous := 0, false, false, false
	for _, mr := range refs {
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM delivery_receipts WHERE device_id=? AND slot=? AND phone=? AND mr=? AND scts>0 AND abs(scts-?)<=300 AND (iccid='' OR iccid=?) AND match_state='ambiguous'`, o.DeviceID, o.Slot, o.Phone, mr, o.DispatchedAt, o.ICCID).Scan(&count); e != nil {
			return o, e
		}
		if count > 0 {
			ambiguous = true
		}
		var report sql.NullInt64
		e = tx.QueryRowContext(ctx, `SELECT max(report_ts) FROM delivery_receipts WHERE matched_operation=? AND mr=?`, o.ID, mr).Scan(&report)
		if e != nil {
			return o, e
		}
		if !report.Valid {
			continue
		} // no matched report
		// Equal timestamp conflicting reports stay unknown; arrival order is not evidence.
		var lo, hi int
		e = tx.QueryRowContext(ctx, `SELECT min(status),max(status) FROM delivery_receipts WHERE matched_operation=? AND mr=? AND report_ts=?`, o.ID, mr, report.Int64).Scan(&lo, &hi)
		if e != nil {
			return o, e
		}
		if lo != hi || report.Int64 <= 0 {
			ambiguous = true
			continue
		}
		o.DeliveryReceived++
		switch {
		case lo == 0:
			success++
		case lo >= 96 && lo <= 127:
			temporary = true
		default:
			failed = true
		}
	}
	switch {
	case ambiguous:
		o.Delivery = "ambiguous"
	case failed:
		o.Delivery = "failed"
	case success == len(refs):
		o.Delivery = "delivered"
	case temporary:
		o.Delivery = "delivering"
	case o.DeliveryReceived > 0:
		o.Delivery = "partial"
	}
	return o, nil
}
