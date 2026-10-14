// Package diagnostics records bounded, best-effort operational evidence.
// It never receives frames, SMS content, credentials, addresses or raw errors.
package diagnostics

type Definition struct {
	Level    string `json:"level"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

var catalog = map[string]Definition{
	"board_parameter_command":         {"info", "query", "板子参数命令已尝试下发"},
	"board_parameter_verified":        {"info", "query", "板子参数修改已读回确认"},
	"status_alert_observation_failed": {"warn", "notification", "持续异常观察暂时失败"},
	"card_notifications_changed":      {"info", "notification", "卡通知开关已更新"},
	"service_started":                 {"info", "service", "应用已启动"},
	"service_stopped":                 {"info", "service", "应用已停止"},
	"configuration_rejected":          {"error", "service", "运行配置未通过校验"},
	"settings_saved":                  {"info", "service", "系统配置已保存，重启应用后生效"},
	"settings_restart_requested":      {"info", "service", "已请求正常重启应用，使系统配置生效"},
	"settings_restart_failed":         {"error", "service", "应用重新启动失败，需要检查本地启动入口"},
	"database_startup_failed":         {"error", "service", "数据库启动失败"},
	"tcp_listener_failed":             {"error", "service", "设备监听失败"},
	"http_listener_failed":            {"error", "service", "网页监听失败"},
	"logging_write_failed":            {"error", "service", "日志文件写入失败；最近记录仍保留在内存"},
	"logging_capacity_reached":        {"warn", "service", "日志容量已满，暂停磁盘新增记录以保护保留期内日志；最近记录仍在内存"},
	"logging_startup_failed":          {"error", "service", "日志文件无法启用；最近记录仅保留在内存"},
	"logging_restore_skipped":         {"warn", "service", "恢复时跳过损坏的诊断记录"},
	"tcp_connected":                   {"info", "tcp", "收到设备连接，等待识别"},
	"tcp_bound":                       {"info", "tcp", "连接已通过配置的设备 ID 检查；不代表设备认证"},
	"tcp_source_rejected":             {"warn", "tcp", "连接来源不在允许范围"},
	"tcp_connection_limit":            {"warn", "tcp", "临时连接数达到上限"},
	"tcp_peer_closed":                 {"info", "tcp", "设备关闭连接"},
	"tcp_idle_timeout":                {"warn", "tcp", "连接读取超时"},
	"tcp_frame_invalid":               {"warn", "tcp", "报文分帧失败或长度超限"},
	"tcp_read_failed":                 {"warn", "tcp", "连接读取失败"},
	"tcp_decode_failed":               {"warn", "tcp", "加密封装解码失败；需核对模式、密钥与报文"},
	"tcp_parse_failed":                {"warn", "tcp", "报文结构不符合当前协议解析要求"},
	"tcp_identity_rejected":           {"warn", "tcp", "报文设备 ID 与配置不匹配"},
	"tcp_session_replaced":            {"info", "tcp", "当前连接被新连接替换"},
	"tcp_session_retired":             {"info", "tcp", "旧连接已停止处理事件"},
	"tcp_shutdown":                    {"info", "tcp", "应用停止，关闭连接"},
	"event_persistence_failed":        {"error", "tcp", "事件入库失败；连接关闭，未宣称可靠收到"},
	"event_received":                  {"debug", "tcp", "收到并持久化协议事件"},
	"event_time_old":                  {"warn", "tcp", "报文时间早于服务器超过12小时，可能为补报或重放；业务继续处理"},
	"event_time_future":               {"warn", "tcp", "报文时间晚于服务器超过10分钟，可能为设备时钟异常；业务继续处理"},
	"event_time_missing":              {"info", "tcp", "报文缺少有效时间戳，无法判断新旧；业务继续处理"},
	"handshake_write_failed":          {"warn", "tcp", "握手应答写入失败"},
	"query_started":                   {"info", "query", "已开始状态查询"},
	"query_completed":                 {"info", "query", "状态查询已收到对应结果"},
	"query_failed":                    {"warn", "query", "状态查询未完成"},
	"command_response":                {"info", "query", "收到命令响应；接受不等于处理完成或短信送达"},
	"sms_preview_created":             {"info", "sms", "已保存发送预览，尚未发送短信"},
	"sms_dispatch_claimed":            {"info", "sms", "已持久化认领发送操作；不会自动重复下发"},
	"sms_write_completed":             {"info", "sms", "单次发送报文写入完成；不等于送达"},
	"sms_write_unknown":               {"warn", "sms", "发送写入结果未知；不会自动重发"},
	"sms_result_persistence_failed":   {"error", "sms", "发送结果保存失败；只查询原操作，不补发"},
	"sms_report_received":             {"info", "sms", "收到短信提交或送达报告；最终状态以发件箱匹配结果为准"},
	"notification_attempt":            {"debug", "notification", "后台尝试推送原通知任务"},
	"notification_accepted":           {"info", "notification", "Bark 接受请求；iPhone 到达情况待核对"},
	"notification_failed":             {"warn", "notification", "通知请求未被确认接受"},
	"notification_queue_failed":       {"error", "notification", "通知队列读取失败"},
	"notification_persistence_failed": {"error", "notification", "通知结果保存失败；后续尝试可能重复通知"},
}

var outcomes = map[string]bool{
	"reported_success": true, "reported_failure": true,
	"device_offline": true, "query_in_progress": true, "device_write_failed": true,
	"device_query_failed": true, "device_result_unknown": true, "request_canceled": true,
	"transport_failed": true, "http_rejected": true, "invalid_response": true,
	"bark_rejected": true, "accepted": true,
}

func Catalog() map[string]Definition {
	copy := make(map[string]Definition, len(catalog))
	for key, value := range catalog {
		copy[key] = value
	}
	return copy
}
