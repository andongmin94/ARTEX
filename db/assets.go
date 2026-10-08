package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

// =====================================================================
// 统一资产表
// =====================================================================

// Asset is a row in the assets table.
type Asset struct {
	ID         int64   `json:"id"`
	Type       string  `json:"type"`
	CompanyID  *int64  `json:"company_id,omitempty"`
	TaskIDs    []int64 `json:"task_ids"`
	Domain     string  `json:"domain,omitempty"`
	RootDomain string  `json:"root_domain,omitempty"`
	IP         string  `json:"ip,omitempty"`
	CSegment   string  `json:"c_segment,omitempty"`
	Port       *int    `json:"port,omitempty"`
	ICP        string  `json:"icp,omitempty"`
	// ip fields
	BoundDomains []string         `json:"bound_domains,omitempty"`
	OpenPorts    []map[string]any `json:"open_ports,omitempty"`
	// subdomain fields
	RecordType  string   `json:"record_type,omitempty"`
	RecordValue []string `json:"record_value,omitempty"`
	// app fields
	BundleID       string `json:"bundle_id,omitempty"`
	AppName        string `json:"app_name,omitempty"`
	Category       string `json:"category,omitempty"`
	AppDescription string `json:"app_description,omitempty"`
	AppICP         string `json:"app_icp,omitempty"`
	// service fields
	URL           string           `json:"url,omitempty"`
	ServiceType   string           `json:"service_type,omitempty"`
	ServiceName   string           `json:"service_name,omitempty"`
	FaviconMMH3   string           `json:"favicon_mmh3,omitempty"`
	StatusCode    *int             `json:"status_code,omitempty"`
	ContentLength *int64           `json:"content_length,omitempty"`
	PageTitle     string           `json:"page_title,omitempty"`
	Technologies  []string         `json:"technologies,omitempty"`
	Auth          []map[string]any `json:"auth,omitempty"`
	// endpoint fields
	Method string           `json:"method,omitempty"`
	Params []map[string]any `json:"params,omitempty"`
	// meta
	Extra             map[string]any `json:"extra,omitempty"`
	LastSeen          string         `json:"last_seen"`
	TaskSource        string         `json:"task_source,omitempty"`
	TaskSourceSummary string         `json:"task_source_summary,omitempty"`
	TaskSourceNodeID  *int64         `json:"task_source_node_id,omitempty"`
}

// AuthItem is one entry in the auth array.
type AuthItem struct {
	Type        string `json:"type,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	Token       string `json:"token,omitempty"`
	Description string `json:"description,omitempty"`
}

// ParamItem is one entry in the params array.
type ParamItem struct {
	Location string `json:"location"`
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Type     string `json:"type,omitempty"`
}

// PortService is one entry in open_ports: {"port":22,"service":"ssh"}.
type PortService struct {
	Port    int    `json:"port"`
	Service string `json:"service,omitempty"`
}

// AssetStore operates on the assets table.
type AssetStore struct {
	db      *DB
	company *CompanyStore
	tx      *sql.Tx
}

// Assets returns the asset store.
func (d *DB) Assets() *AssetStore {
	return &AssetStore{db: d, company: d.Companies()}
}

// Companies returns the company store associated with this asset store.
func (s *AssetStore) Companies() *CompanyStore { return s.company }

// withCompanyScopeMutation serializes scope resolution and every asset write
// that consumes its result in one transaction. Nested asset side effects reuse
// the same transaction through the scoped store.
func (s *AssetStore) withCompanyScopeMutation(fn func(*AssetStore) (int64, error)) (int64, error) {
	if s.tx != nil {
		return fn(s)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	scoped := &AssetStore{db: s.db, company: s.company, tx: tx}
	id, err := fn(scoped)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *AssetStore) resolveCompanyWithICP(rootDomain, ipStr, icp string) (*int64, error) {
	if s.tx != nil {
		return resolveCompanyWithICP(s.tx, rootDomain, ipStr, icp)
	}
	return s.company.ResolveCompanyWithICP(rootDomain, ipStr, icp)
}

// =====================================================================
// Helpers
// =====================================================================

// calcCSegment computes the /24 (IPv4) or /48 (IPv6) network for an IP string.
func calcCSegment(ipStr string) string {
	if ipStr == "" {
		return ""
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	if ip.To4() != nil {
		// IPv4 /24
		parts := strings.Split(ipStr, ".")
		if len(parts) == 4 {
			return parts[0] + "." + parts[1] + "." + parts[2] + ".0/24"
		}
		return ""
	}
	// IPv6 /48
	_, ipnet, err := net.ParseCIDR(ipStr + "/48")
	if err != nil {
		return ""
	}
	return ipnet.String()
}

// normalizeURL lowercases scheme and host, strips trailing slash from bare roots.
func normalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	s := u.String()
	if strings.HasSuffix(s, "/") && u.Path == "/" && u.RawQuery == "" {
		s = strings.TrimSuffix(s, "/")
	}
	return s
}

// parseURL extracts domain, port, service_name from a URL.
func parseURL(raw string) (domain string, port int, serviceName string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0, ""
	}
	domain = strings.ToLower(u.Hostname())
	port = defaultPort(strings.ToLower(u.Scheme), u.Port())
	switch strings.ToLower(u.Scheme) {
	case "https":
		serviceName = "HTTPS"
	case "http":
		serviceName = "HTTP"
	default:
		serviceName = strings.ToUpper(u.Scheme)
	}
	return
}

// nullableInt64 returns sql.NullInt64.
func nullableInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

// =====================================================================
// UpsertRootDomain
// =====================================================================

// UpsertRootDomainReq is the input for UpsertRootDomain.
type UpsertRootDomainReq struct {
	Domain string
	ICP    string
	TaskID int64
}

// UpsertRootDomain idempotently inserts or merges a root domain asset.
func (s *AssetStore) UpsertRootDomain(req UpsertRootDomainReq) (int64, error) {
	domain := DomainKey(req.Domain)
	if domain == "" {
		return 0, fmt.Errorf("domain is required")
	}
	if s.tx == nil {
		return s.withCompanyScopeMutation(func(scoped *AssetStore) (int64, error) { return scoped.UpsertRootDomain(req) })
	}
	return s.saveAsset(&Asset{Type: "root_domain", Domain: domain, RootDomain: domain, ICP: req.ICP}, req.TaskID, false)
}

// ErrAssetIPInvalid marks a non-address value in an asset's ip field. Both
// insert_assets and the asset API report it per item with the item index, so one
// bad entry never costs the rest of the batch.
var ErrAssetIPInvalid = errors.New("invalid asset ip")

// ValidateAssetIP keeps hostnames out of assets.ip so network scope and
// attribution operate on addresses. Empty values are accepted for optional
// service and endpoint IP fields; the API reports invalid items individually.
func ValidateAssetIP(value string) error {
	if value == "" || net.ParseIP(value) != nil {
		return nil
	}
	return fmt.Errorf(
		"%w: ip는 IPv4/IPv6 주소여야 합니다. 입력값: %q. 호스트 이름이면 type=subdomain의 domain 필드를 사용하세요;"+
			"주소를 등록하려면 먼저 A/AAAA 레코드를 조회하고 해당 주소를 ip에 입력하세요",
		ErrAssetIPInvalid, value)
}

// =====================================================================
// UpsertIP
// =====================================================================

// UpsertIPReq is the input for UpsertIP.
type UpsertIPReq struct {
	IP           string
	BoundDomains []string
	OpenPorts    []PortService
	TaskID       int64
}

// UpsertIP idempotently inserts or merges an IP asset.
func (s *AssetStore) UpsertIP(req UpsertIPReq) (int64, error) {
	ip, _, _, err := sqliteAddress(req.IP)
	if err != nil {
		return 0, err
	}
	if ip == "" {
		return 0, fmt.Errorf("ip is required")
	}
	if s.tx == nil {
		return s.withCompanyScopeMutation(func(scoped *AssetStore) (int64, error) { return scoped.UpsertIP(req) })
	}
	ports := make([]map[string]any, 0, len(req.OpenPorts))
	for _, port := range req.OpenPorts {
		if port.Port < 1 || port.Port > 65535 {
			return 0, fmt.Errorf("port must be between 1 and 65535")
		}
		ports = append(ports, map[string]any{"port": port.Port, "service": port.Service})
	}
	return s.saveAsset(&Asset{Type: "ip", IP: ip, CSegment: calcCSegment(ip), BoundDomains: req.BoundDomains, OpenPorts: ports}, req.TaskID, false)
}

// AppendIPPort appends a {port, service} entry to an existing IP asset's open_ports.
func (s *AssetStore) AppendIPPort(ip string, port int, service string) error {
	_, err := s.UpsertIP(UpsertIPReq{IP: ip, OpenPorts: []PortService{{Port: port, Service: service}}})
	return err
}

// AppendIPBoundDomain appends a domain to an existing IP asset's bound_domains.
func (s *AssetStore) AppendIPBoundDomain(ip, domain string) error {
	_, err := s.UpsertIP(UpsertIPReq{IP: ip, BoundDomains: []string{DomainKey(domain)}})
	return err
}

// =====================================================================
// UpsertSubdomain
// =====================================================================

// UpsertSubdomainReq is the input for UpsertSubdomain.
type UpsertSubdomainReq struct {
	Domain      string
	RecordType  string
	RecordValue []string
	ICP         string
	TaskID      int64
}

// UpsertSubdomain idempotently inserts or merges a subdomain asset and triggers
// side effects: root_domain upsert + IP bound_domains update.
func (s *AssetStore) UpsertSubdomain(req UpsertSubdomainReq) (int64, error) {
	domain := DomainKey(req.Domain)
	if domain == "" {
		return 0, fmt.Errorf("domain is required")
	}
	if s.tx == nil {
		return s.withCompanyScopeMutation(func(scoped *AssetStore) (int64, error) { return scoped.UpsertSubdomain(req) })
	}
	root, _ := RootDomain(domain)
	if root == "" {
		root = domain
	}
	if _, err := s.UpsertRootDomain(UpsertRootDomainReq{Domain: root, TaskID: req.TaskID}); err != nil {
		return 0, err
	}
	var ip string
	if req.RecordType == "A" || req.RecordType == "AAAA" {
		for _, value := range req.RecordValue {
			for _, part := range strings.Split(value, ",") {
				normalized, _, _, err := sqliteAddress(strings.TrimSpace(part))
				if err != nil || normalized == "" {
					continue
				}
				if ip == "" {
					ip = normalized
				}
				if _, err := s.UpsertIP(UpsertIPReq{IP: normalized, BoundDomains: []string{domain}, TaskID: req.TaskID}); err != nil {
					return 0, err
				}
			}
		}
	}
	return s.saveAsset(&Asset{Type: "subdomain", Domain: domain, RootDomain: root, RecordType: req.RecordType, RecordValue: req.RecordValue, IP: ip, CSegment: calcCSegment(ip), ICP: req.ICP}, req.TaskID, false)
}

// =====================================================================
// UpsertApp
// =====================================================================

// UpsertAppReq is the input for UpsertApp.
type UpsertAppReq struct {
	Name        string
	BundleID    string
	Category    string
	Description string
	ICP         string
	CompanyID   *int64 // explicit override; nil = exact ICP auto-attribution when available
	TaskID      int64
}

// UpsertApp idempotently inserts or merges an app asset.
func (s *AssetStore) UpsertApp(req UpsertAppReq) (int64, error) {
	if req.Name == "" {
		return 0, fmt.Errorf("app name is required")
	}
	if s.tx == nil {
		return s.withCompanyScopeMutation(func(scoped *AssetStore) (int64, error) { return scoped.UpsertApp(req) })
	}
	return s.saveAsset(&Asset{Type: "app", AppName: req.Name, BundleID: req.BundleID, Category: req.Category, AppDescription: req.Description, AppICP: req.ICP, CompanyID: req.CompanyID}, req.TaskID, req.CompanyID != nil)
}

// =====================================================================
// UpsertHTTPService
// =====================================================================

// UpsertHTTPServiceReq is the input for UpsertHTTPService.
type UpsertHTTPServiceReq struct {
	URL           string
	Technologies  []string
	StatusCode    *int
	ContentLength *int64
	PageTitle     string
	FaviconMMH3   string
	Auth          []map[string]any
	IP            string // optional, from async DNS
	TaskID        int64
}

// UpsertHTTPService inserts or merges an HTTP service asset. Domain, port,
// service_name, and root_domain are auto-extracted from URL.
func (s *AssetStore) UpsertHTTPService(req UpsertHTTPServiceReq) (int64, error) {
	if req.URL == "" {
		return 0, fmt.Errorf("url is required")
	}
	if err := ValidateAssetIP(req.IP); err != nil {
		return 0, err
	}
	if s.tx == nil {
		return s.withCompanyScopeMutation(func(scoped *AssetStore) (int64, error) { return scoped.UpsertHTTPService(req) })
	}
	raw := normalizeURL(req.URL)
	domain, port, service := parseURL(raw)
	root, _ := RootDomain(domain)
	if root == "" {
		root = domain
	}
	a := &Asset{Type: "service", ServiceType: "http", URL: raw, Domain: domain, RootDomain: root, IP: req.IP, CSegment: calcCSegment(req.IP), ServiceName: service, Technologies: req.Technologies, StatusCode: req.StatusCode, ContentLength: req.ContentLength, PageTitle: req.PageTitle, FaviconMMH3: req.FaviconMMH3, Auth: req.Auth}
	if port > 0 {
		a.Port = &port
	}
	id, err := s.saveAsset(a, req.TaskID, false)
	if err != nil {
		return 0, err
	}
	if err := s.linkHostAssets(domain, root, req.TaskID); err != nil {
		return 0, err
	}
	if req.IP != "" {
		var ports []PortService
		if port > 0 {
			ports = []PortService{{Port: port, Service: service}}
		}
		if _, err := s.UpsertIP(UpsertIPReq{IP: req.IP, BoundDomains: []string{domain}, OpenPorts: ports, TaskID: req.TaskID}); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// =====================================================================
// UpsertOtherService
// =====================================================================

// UpsertOtherServiceReq is the input for UpsertOtherService.
type UpsertOtherServiceReq struct {
	Domain      string // domain or ip required
	IP          string
	Port        int
	ServiceName string
	Auth        []map[string]any
	TaskID      int64
}

// UpsertOtherService inserts or merges a non-HTTP service asset.
func (s *AssetStore) UpsertOtherService(req UpsertOtherServiceReq) (int64, error) {
	if req.Domain == "" && req.IP == "" {
		return 0, fmt.Errorf("domain or ip is required")
	}
	if err := ValidateAssetIP(req.IP); err != nil {
		return 0, err
	}
	if req.Port < 1 || req.Port > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535")
	}
	service := strings.ToLower(strings.TrimSpace(req.ServiceName))
	if service == "" {
		return 0, fmt.Errorf("service_name is required")
	}
	if s.tx == nil {
		return s.withCompanyScopeMutation(func(scoped *AssetStore) (int64, error) { return scoped.UpsertOtherService(req) })
	}
	domain := DomainKey(req.Domain)
	root, _ := RootDomain(domain)
	if root == "" {
		root = domain
	}
	port := req.Port
	id, err := s.saveAsset(&Asset{Type: "service", ServiceType: "other", ServiceName: service, Domain: domain, RootDomain: root, IP: req.IP, Port: &port, CSegment: calcCSegment(req.IP), Auth: req.Auth}, req.TaskID, false)
	if err != nil {
		return 0, err
	}
	if req.IP != "" {
		if _, err := s.UpsertIP(UpsertIPReq{IP: req.IP, BoundDomains: []string{domain}, OpenPorts: []PortService{{Port: port, Service: service}}, TaskID: req.TaskID}); err != nil {
			return 0, err
		}
	}
	if err := s.linkHostAssets(domain, root, req.TaskID); err != nil {
		return 0, err
	}
	return id, nil
}

// linkHostAssets ensures a service/endpoint's host is also registered as its own
// root_domain and (when it's a real subdomain, not the apex or an IP) subdomain
// asset — so those asset types stay populated and can anchor task scope. Best-effort.
func (s *AssetStore) linkHostAssets(domain, root string, taskID int64) error {
	if root != "" && net.ParseIP(root) == nil {
		if _, err := s.UpsertRootDomain(UpsertRootDomainReq{Domain: root, TaskID: taskID}); err != nil {
			return err
		}
	}
	if domain != "" && domain != root && net.ParseIP(domain) == nil {
		if _, err := s.UpsertSubdomain(UpsertSubdomainReq{Domain: domain, TaskID: taskID}); err != nil {
			return err
		}
	}
	return nil
}

// =====================================================================
// UpsertEndpoint
// =====================================================================

// UpsertEndpointReq is the input for UpsertEndpoint.
type UpsertEndpointReq struct {
	URL    string
	Method string
	Params []map[string]any
	IP     string // optional
	TaskID int64
}

// UpsertEndpoint inserts or merges an endpoint asset. Domain, port, root_domain
// are auto-extracted from the URL.
func (s *AssetStore) UpsertEndpoint(req UpsertEndpointReq) (int64, error) {
	if req.URL == "" || req.Method == "" {
		return 0, fmt.Errorf("url and method are required")
	}
	if err := ValidateAssetIP(req.IP); err != nil {
		return 0, err
	}
	if s.tx == nil {
		return s.withCompanyScopeMutation(func(scoped *AssetStore) (int64, error) { return scoped.UpsertEndpoint(req) })
	}
	raw := normalizeURL(req.URL)
	domain, port, _ := parseURL(raw)
	root, _ := RootDomain(domain)
	if root == "" {
		root = domain
	}
	a := &Asset{Type: "endpoint", URL: raw, Method: strings.ToUpper(req.Method), Domain: domain, RootDomain: root, IP: req.IP, CSegment: calcCSegment(req.IP), Params: req.Params}
	if port > 0 {
		a.Port = &port
	}
	id, err := s.saveAsset(a, req.TaskID, false)
	if err != nil {
		return 0, err
	}
	if err := s.linkHostAssets(domain, root, req.TaskID); err != nil {
		return 0, err
	}
	if req.IP != "" {
		if _, err := s.UpsertIP(UpsertIPReq{IP: req.IP, TaskID: req.TaskID}); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// =====================================================================
// Query helpers
// =====================================================================

// QueryByType returns assets rows of a given type, newest first.
func (s *AssetStore) QueryByType(typ string, limit, offset int) ([]*Asset, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(assetSelectCols+`
WHERE type = $1
ORDER BY last_seen DESC, id DESC
LIMIT $2 OFFSET $3`, typ, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAssets(rows)
}

// CountByType returns the total number of assets of a type (for server-side pagination).
func (s *AssetStore) CountByType(typ string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM assets WHERE type = $1`, typ).Scan(&n)
	return n, err
}

// QueryByCompany returns assets for a company, optionally filtered by type.
// limit <= 0 means no limit.
func (s *AssetStore) QueryByCompany(companyID int64, typ string, limit, offset int) ([]*Asset, error) {
	q := assetSelectCols + ` WHERE company_id = $1`
	args := []any{companyID}
	if typ != "" {
		args = append(args, typ)
		q += fmt.Sprintf(` AND type = $%d`, len(args))
	}
	q += pageClause(&args, limit, offset)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAssets(rows)
}

func (s *AssetStore) CountByCompany(companyID int64, typ string) (int, error) {
	q := `SELECT count(*) FROM assets WHERE company_id = $1`
	args := []any{companyID}
	if typ != "" {
		args = append(args, typ)
		q += fmt.Sprintf(` AND type = $%d`, len(args))
	}
	var n int
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n, err
}

func pageClause(args *[]any, limit, offset int) string {
	q := ` ORDER BY last_seen DESC, id DESC`
	if limit > 0 {
		*args = append(*args, limit)
		q += fmt.Sprintf(` LIMIT $%d`, len(*args))
	}
	if offset > 0 {
		if limit <= 0 {
			q += ` LIMIT -1`
		}
		*args = append(*args, offset)
		q += fmt.Sprintf(` OFFSET $%d`, len(*args))
	}
	return q
}

// QueryByTask returns assets linked to the given task.
func (s *AssetStore) QueryByTask(taskID int64, typ string, limit, offset int) ([]*Asset, error) {
	q := assetSelectCols + ` WHERE EXISTS (SELECT 1 FROM task_asset_links link WHERE link.asset_id=assets.id AND link.task_id=$1)`
	args := []any{taskID}
	if typ != "" {
		args = append(args, typ)
		q += fmt.Sprintf(` AND type = $%d`, len(args))
	}
	q += pageClause(&args, limit, offset)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets, err := scanAssets(rows)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateTaskAssetSources(taskID, assets); err != nil {
		return nil, err
	}
	return assets, nil
}

func (s *AssetStore) CountByTask(taskID int64, typ string) (int, error) {
	q := `SELECT count(*) FROM assets WHERE EXISTS (SELECT 1 FROM task_asset_links link WHERE link.asset_id=assets.id AND link.task_id=$1)`
	args := []any{taskID}
	if typ != "" {
		args = append(args, typ)
		q += fmt.Sprintf(` AND type = $%d`, len(args))
	}
	var n int
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n, err
}

func (s *AssetStore) CountsByTypeForTask(taskID int64) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT type, COUNT(*) FROM assets WHERE EXISTS (SELECT 1 FROM task_asset_links link WHERE link.asset_id=assets.id AND link.task_id=$1) GROUP BY type`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTypeCounts(rows)
}

// DeleteByTaskID removes assets owned only by taskID and detaches taskID from
// assets shared with other tasks. Full task deletion uses the coordinated
// transaction in DeleteTaskCascadePrepared; this method remains for callers
// that explicitly manage only asset associations.
func (s *AssetStore) DeleteByTaskID(taskID int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM assets WHERE id IN (SELECT asset_id FROM task_asset_links WHERE task_id=?1) AND NOT EXISTS (SELECT 1 FROM task_asset_links link WHERE link.asset_id=assets.id AND link.task_id<>?1) AND NOT EXISTS (SELECT 1 FROM exploration_anchors anchor JOIN exploration_nodes node ON node.id=anchor.node_id JOIN tasks task ON task.exploration_id=node.exploration_id WHERE anchor.asset_id=assets.id AND task.id<>?1 AND task.deleted_at IS NULL)`, taskID)
	if err != nil {
		return 0, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM task_asset_links WHERE task_id=?1`, taskID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// HostsByTask returns the exact HTTP host candidates attached to a task's
// assets. Domain/IP columns cover root domains, subdomains and non-HTTP
// services; URL covers HTTP services and endpoints.
func (s *AssetStore) HostsByTask(taskID int64) ([]string, error) {
	rows, err := s.db.Query(`
SELECT COALESCE(domain,''), COALESCE(ip,''), COALESCE(url,'')
FROM assets WHERE EXISTS (SELECT 1 FROM task_asset_links link WHERE link.asset_id=assets.id AND link.task_id=$1)`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hosts := make(map[string]struct{})
	add := func(host string) {
		host = strings.TrimSpace(strings.ToLower(host))
		if host != "" {
			hosts[host] = struct{}{}
		}
	}
	for rows.Next() {
		var domain, ip, rawURL string
		if err := rows.Scan(&domain, &ip, &rawURL); err != nil {
			return nil, err
		}
		add(domain)
		add(ip)
		if u, err := url.Parse(rawURL); err == nil {
			add(u.Hostname())
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hosts))
	for host := range hosts {
		out = append(out, host)
	}
	sort.Strings(out)
	return out, nil
}

// HostsForTaskDeletion returns hosts that belong to the task being deleted and
// are not referenced by any other live task. Current-task candidates include
// both task links and exploration anchors so anchor-only assets (which
// were anchor-only) are covered. Protection is host-wide: if another live task
// references any asset for a candidate host, that host's global traffic remains.
func (s *AssetStore) HostsForTaskDeletion(taskID, explorationID int64) ([]string, error) {
	return hostsForTaskDeletion(s.db, taskID, explorationID)
}

type rowsQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// hostsForTaskDeletion is shared by the read-only AssetStore API and the task
// deletion transaction. Coordinated traffic deletion must call it through the
// transaction path so asset/anchor writes stay locked until the task delete is
// committed.
func hostsForTaskDeletion(q rowsQuerier, taskID, explorationID int64) ([]string, error) {
	rows, err := q.Query(`
WITH current_assets AS (
  SELECT asset_id AS id FROM task_asset_links WHERE task_id=$1
  UNION
  SELECT ea.asset_id
  FROM exploration_anchors ea
  JOIN exploration_nodes n ON n.id=ea.node_id
  WHERE n.exploration_id=$2
),
other_assets AS (
  SELECT DISTINCT a.id
  FROM assets a
  WHERE EXISTS (
    SELECT 1 FROM tasks t
    JOIN task_asset_links link ON link.task_id=t.id
    WHERE t.id<>$1 AND t.deleted_at IS NULL AND link.asset_id=a.id
  ) OR EXISTS (
    SELECT 1
    FROM exploration_anchors ea
    JOIN exploration_nodes n ON n.id=ea.node_id
    JOIN tasks t ON t.exploration_id=n.exploration_id
    WHERE ea.asset_id=a.id AND t.id<>$1 AND t.deleted_at IS NULL
  )
)
SELECT COALESCE(a.domain,''), COALESCE(a.ip,''), COALESCE(a.url,''), true
FROM assets a JOIN current_assets c ON c.id=a.id
UNION ALL
SELECT COALESCE(a.domain,''), COALESCE(a.ip,''), COALESCE(a.url,''), false
FROM assets a JOIN other_assets o ON o.id=a.id`, taskID, explorationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make(map[string]struct{})
	protected := make(map[string]struct{})
	add := func(dst map[string]struct{}, raw string) {
		raw = strings.TrimSpace(strings.ToLower(raw))
		if raw != "" {
			dst[raw] = struct{}{}
		}
	}
	for rows.Next() {
		var domain, ip, rawURL string
		var candidate bool
		if err := rows.Scan(&domain, &ip, &rawURL, &candidate); err != nil {
			return nil, err
		}
		dst := protected
		if candidate {
			dst = candidates
		}
		add(dst, domain)
		add(dst, ip)
		if u, err := url.Parse(rawURL); err == nil {
			add(dst, u.Hostname())
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(candidates))
	for host := range candidates {
		if _, shared := protected[host]; !shared {
			out = append(out, host)
		}
	}
	sort.Strings(out)
	return out, nil
}

const assetSelectCols = `SELECT id,type,company_id,
(SELECT json_group_array(task_id) FROM (SELECT task_id FROM task_asset_links WHERE asset_id=assets.id ORDER BY task_id)),
COALESCE(domain,''),COALESCE(root_domain,''),COALESCE(ip,''),COALESCE(c_segment,''),port,COALESCE(icp,''),
(SELECT json_group_array(domain) FROM (SELECT domain FROM asset_bound_domains WHERE asset_id=assets.id ORDER BY position)),
(SELECT json_group_array(json(item)) FROM (SELECT json_patch(extra,json_object('port',port,'service',service)) AS item FROM asset_open_ports WHERE asset_id=assets.id ORDER BY position)),
COALESCE(record_type,''),
(SELECT json_group_array(value) FROM (SELECT value FROM asset_records WHERE asset_id=assets.id ORDER BY position)),
COALESCE(bundle_id,''),COALESCE(app_name,''),COALESCE(category,''),COALESCE(app_description,''),COALESCE(app_icp,''),
COALESCE(url,''),COALESCE(service_type,''),COALESCE(service_name,''),COALESCE(favicon_mmh3,''),status_code,content_length,COALESCE(page_title,''),
(SELECT json_group_array(technology) FROM (SELECT technology FROM asset_technologies WHERE asset_id=assets.id ORDER BY position)),
auth,COALESCE(method,''),params,extra,CAST(last_seen AS TEXT) FROM assets`

// GetByIDs returns assets with the given ids (order preserved by id array order).
func (s *AssetStore) GetByIDs(ids []int64) ([]*Asset, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	sql := assetSelectCols + " WHERE id IN (" + strings.Join(placeholders, ",") + ") ORDER BY last_seen DESC"
	rows, err := s.db.Query(sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAssets(rows)
}

// DeleteByCompanyID hard-deletes all assets belonging to a company. Returns rows deleted.
func (s *AssetStore) DeleteByCompanyID(companyID int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM assets WHERE company_id = $1`, companyID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteByHost hard-deletes every asset whose host exactly matches the given value:
// root_domain / subdomain / service / endpoint (they carry the host in domain or
// root_domain) plus an ip asset and its services/endpoints (ip column). The host is
// normalized the same way it is stored (DomainKey: lowercase/trim/strip trailing dot)
// so matching is exact, not fuzzy. Passing a root domain also removes its subdomains
// and their services/endpoints (they carry root_domain = that host); passing a
// subdomain/IP removes only that host's own assets. Referencing exploration_anchors
// rows are cleaned by ON DELETE CASCADE. Returns rows deleted, grouped by type.
func (s *AssetStore) DeleteByHost(host string) (map[string]int64, error) {
	h := DomainKey(host)
	if h == "" {
		return nil, fmt.Errorf("host is required")
	}
	rows, err := s.db.Query(`
DELETE FROM assets
WHERE domain = $1 OR root_domain = $1 OR ip = $1
RETURNING type`, h)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		counts[t]++
	}
	return counts, rows.Err()
}

// DeleteByIDs hard-deletes assets by their IDs. Returns the number of rows deleted.
func (s *AssetStore) DeleteByIDs(ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	res, err := s.db.Exec("DELETE FROM assets WHERE id IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountsByType returns asset counts per type.
func (s *AssetStore) CountsByType() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT type, COUNT(*) FROM assets GROUP BY type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTypeCounts(rows)
}

func scanTypeCounts(rows *sql.Rows) (map[string]int, error) {
	out := map[string]int{}
	for rows.Next() {
		var typ string
		var cnt int
		if err := rows.Scan(&typ, &cnt); err != nil {
			return nil, err
		}
		out[typ] = cnt
	}
	return out, rows.Err()
}

// scanAssets scans the wide SELECT that covers all type columns.
func scanAssets(rows *sql.Rows) ([]*Asset, error) {
	var out []*Asset
	for rows.Next() {
		a := &Asset{}
		var taskIDsRaw, boundDomainsRaw, openPortsRaw, recordValueRaw, techsRaw, authRaw, paramsRaw, extraRaw []byte
		var port, statusCode sql.NullInt64
		var contentLength sql.NullInt64
		var companyID sql.NullInt64
		if err := rows.Scan(
			&a.ID, &a.Type, &companyID, &taskIDsRaw,
			&a.Domain, &a.RootDomain, &a.IP, &a.CSegment, &port,
			&a.ICP, &boundDomainsRaw, &openPortsRaw, &a.RecordType,
			&recordValueRaw, &a.BundleID, &a.AppName,
			&a.Category, &a.AppDescription, &a.AppICP,
			&a.URL, &a.ServiceType, &a.ServiceName,
			&a.FaviconMMH3, &statusCode, &contentLength,
			&a.PageTitle, &techsRaw, &authRaw,
			&a.Method, &paramsRaw, &extraRaw, &a.LastSeen,
		); err != nil {
			return nil, err
		}
		if companyID.Valid {
			cid := companyID.Int64
			a.CompanyID = &cid
		}
		if port.Valid {
			p := int(port.Int64)
			a.Port = &p
		}
		if statusCode.Valid {
			sc := int(statusCode.Int64)
			a.StatusCode = &sc
		}
		if contentLength.Valid {
			cl := contentLength.Int64
			a.ContentLength = &cl
		}
		for _, field := range []struct {
			raw  []byte
			dest any
		}{
			{taskIDsRaw, &a.TaskIDs}, {boundDomainsRaw, &a.BoundDomains}, {openPortsRaw, &a.OpenPorts},
			{recordValueRaw, &a.RecordValue}, {techsRaw, &a.Technologies}, {authRaw, &a.Auth},
			{paramsRaw, &a.Params}, {extraRaw, &a.Extra},
		} {
			if err := json.Unmarshal(field.raw, field.dest); err != nil {
				return nil, fmt.Errorf("asset %d JSON: %w", a.ID, err)
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
