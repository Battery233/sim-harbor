package store

import "strings"

func notificationSourceStreams(source string) []int {
	switch source {
	case "sms":
		return []int{0}
	case "call":
		return []int{1}
	case "system":
		return []int{2, 3}
	case "everything":
		return []int{0, 1, 2, 3}
	default: // The caller validates source; legacy "all" means SMS and calls.
		return []int{0, 1}
	}
}

// Aggregate each selected stream before combining. No record metadata, sort or
// history projection is needed for counters. Keep joins that exclude orphans.
func notificationCountsSQL(source string, pendingOnly bool) string {
	tables := []string{
		"notifications n JOIN messages m ON m.id=n.message_id",
		"call_notifications n JOIN calls c ON c.id=n.call_id",
		"status_alert_jobs n",
		"system_notifications n",
	}
	parts := []string{}
	for _, i := range notificationSourceStreams(source) {
		fields := "count(*) pending,coalesce(sum(n.attempts>0),0) retrying"
		where := " WHERE n.state='pending'"
		if !pendingOnly {
			fields = "coalesce(sum(n.state='pending'),0) pending,coalesce(sum(n.state='pending' AND n.attempts>0),0) retrying,coalesce(sum(n.state='sent'),0) accepted,coalesce(sum(n.state='restore_held'),0) held"
			where = ""
		}
		parts = append(parts, "SELECT "+fields+" FROM "+tables[i]+where)
	}
	fields := "coalesce(sum(pending),0),coalesce(sum(retrying),0)"
	if !pendingOnly {
		fields += ",coalesce(sum(accepted),0),coalesce(sum(held),0)"
	}
	return "SELECT " + fields + " FROM (" + strings.Join(parts, " UNION ALL ") + ")"
}
