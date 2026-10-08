package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// saveAsset merges one natural-key asset and its relations inside the caller's
// IMMEDIATE transaction. Task membership has exactly one source: task_asset_links.
func (s *AssetStore) saveAsset(in *Asset, taskID int64, explicitCompany bool) (int64, error) {
	if s.tx == nil {
		return 0, errors.New("자산 저장에는 업무 트랜잭션이 필요합니다")
	}
	if in.IP != "" {
		normalized, _, _, err := sqliteAddress(in.IP)
		if err != nil {
			return 0, err
		}
		in.IP = normalized
		in.CSegment = calcCSegment(normalized)
	}
	where := ""
	var key []any
	switch in.Type {
	case "root_domain":
		where, key = "type='root_domain' AND domain=?1", []any{in.Domain}
	case "ip":
		where, key = "type='ip' AND ip=?1", []any{in.IP}
	case "subdomain":
		where, key = "type='subdomain' AND domain=?1 AND COALESCE(record_type,'')=?2", []any{in.Domain, in.RecordType}
	case "app":
		if in.BundleID != "" {
			where, key = "type='app' AND bundle_id=?1", []any{in.BundleID}
		} else {
			where, key = "type='app' AND bundle_id IS NULL AND app_name=?1", []any{in.AppName}
		}
	case "service":
		if in.ServiceType == "http" {
			where, key = "type='service' AND service_type='http' AND url=?1", []any{in.URL}
		} else {
			where, key = "type='service' AND service_type='other' AND COALESCE(domain,'')=?1 AND COALESCE(ip,'')=?2 AND port=?3 AND service_name=?4", []any{in.Domain, in.IP, in.Port, in.ServiceName}
		}
	case "endpoint":
		where, key = "type='endpoint' AND url=?1 AND method=?2", []any{in.URL, in.Method}
	default:
		return 0, fmt.Errorf("지원하지 않는 자산 유형: %s", in.Type)
	}
	var id int64
	var source string
	err := s.tx.QueryRow(`SELECT id,company_source FROM assets WHERE `+where, key...).Scan(&id, &source)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	a := &Asset{Type: in.Type, Extra: map[string]any{}}
	if err == nil {
		rows, err := s.tx.Query(assetSelectCols+` WHERE id=?1`, id)
		if err != nil {
			return 0, err
		}
		old, scanErr := scanAssets(rows)
		if err := errors.Join(scanErr, rows.Close()); err != nil {
			return 0, err
		}
		if len(old) != 1 {
			return 0, errors.New("병합할 자산이 없습니다")
		}
		a = old[0]
	}
	for _, field := range []struct {
		target *string
		value  string
	}{
		{&a.Domain, in.Domain}, {&a.RootDomain, in.RootDomain}, {&a.IP, in.IP}, {&a.CSegment, in.CSegment},
		{&a.ICP, in.ICP}, {&a.RecordType, in.RecordType}, {&a.BundleID, in.BundleID}, {&a.AppName, in.AppName},
		{&a.Category, in.Category}, {&a.AppDescription, in.AppDescription}, {&a.AppICP, in.AppICP},
		{&a.URL, in.URL}, {&a.ServiceType, in.ServiceType}, {&a.ServiceName, in.ServiceName}, {&a.FaviconMMH3, in.FaviconMMH3},
		{&a.PageTitle, in.PageTitle}, {&a.Method, in.Method},
	} {
		if field.value != "" {
			*field.target = field.value
		}
	}
	if in.Port != nil {
		a.Port = in.Port
	}
	if in.StatusCode != nil {
		a.StatusCode = in.StatusCode
	}
	if in.ContentLength != nil {
		a.ContentLength = in.ContentLength
	}
	if explicitCompany {
		a.CompanyID = in.CompanyID
		source = "explicit"
	} else if source != "explicit" || a.CompanyID == nil {
		companyID, err := s.resolveCompanyWithICP(a.RootDomain, a.IP, a.ICP)
		if err != nil {
			return 0, err
		}
		if companyID == nil && a.AppICP != "" {
			companyID, err = s.resolveCompanyWithICP(a.RootDomain, a.IP, a.AppICP)
			if err != nil {
				return 0, err
			}
		}
		a.CompanyID = companyID
		source = "scope"
	}
	a.BoundDomains = mergeAssetStrings(a.BoundDomains, in.BoundDomains)
	a.RecordValue = mergeAssetStrings(a.RecordValue, in.RecordValue)
	a.Technologies = mergeAssetStrings(a.Technologies, in.Technologies)
	a.Auth, err = mergeAssetObjects(a.Auth, in.Auth, false)
	if err != nil {
		return 0, err
	}
	a.Params, err = mergeAssetObjects(a.Params, in.Params, true)
	if err != nil {
		return 0, err
	}
	a.OpenPorts = mergeAssetPorts(a.OpenPorts, in.OpenPorts)
	auth, err := json.Marshal(nonNilAssetObjects(a.Auth))
	if err != nil {
		return 0, err
	}
	params, err := json.Marshal(nonNilAssetObjects(a.Params))
	if err != nil {
		return 0, err
	}
	extra, err := json.Marshal(a.Extra)
	if err != nil {
		return 0, err
	}
	var primaryID any
	if id > 0 {
		primaryID = id
	}
	_, family, address, err := sqliteAddress(a.IP)
	if err != nil {
		return 0, err
	}
	var familyValue any
	if family > 0 {
		familyValue = family
	}
	err = s.tx.QueryRow(`INSERT INTO assets(id,type,company_id,company_source,domain,root_domain,ip,c_segment,port,icp,record_type,bundle_id,app_name,category,app_description,app_icp,url,service_type,service_name,favicon_mmh3,status_code,content_length,page_title,auth,method,params,extra,ip_family,ip_address)
VALUES (?1,?2,?3,?4,NULLIF(?5,''),NULLIF(?6,''),NULLIF(?7,''),NULLIF(?8,''),?9,NULLIF(?10,''),NULLIF(?11,''),NULLIF(?12,''),NULLIF(?13,''),NULLIF(?14,''),NULLIF(?15,''),NULLIF(?16,''),NULLIF(?17,''),NULLIF(?18,''),NULLIF(?19,''),NULLIF(?20,''),?21,?22,NULLIF(?23,''),?24,NULLIF(?25,''),?26,?27,?28,?29)
ON CONFLICT(id) DO UPDATE SET company_id=excluded.company_id,company_source=excluded.company_source,domain=excluded.domain,root_domain=excluded.root_domain,ip=excluded.ip,c_segment=excluded.c_segment,port=excluded.port,icp=excluded.icp,record_type=excluded.record_type,bundle_id=excluded.bundle_id,app_name=excluded.app_name,category=excluded.category,app_description=excluded.app_description,app_icp=excluded.app_icp,url=excluded.url,service_type=excluded.service_type,service_name=excluded.service_name,favicon_mmh3=excluded.favicon_mmh3,status_code=excluded.status_code,content_length=excluded.content_length,page_title=excluded.page_title,auth=excluded.auth,method=excluded.method,params=excluded.params,extra=excluded.extra,ip_family=excluded.ip_family,ip_address=excluded.ip_address,last_seen=strftime('%Y-%m-%d %H:%M:%f','now') RETURNING id`,
		primaryID, a.Type, a.CompanyID, source, a.Domain, a.RootDomain, a.IP, a.CSegment, a.Port, a.ICP, a.RecordType, a.BundleID, a.AppName, a.Category, a.AppDescription, a.AppICP, a.URL, a.ServiceType, a.ServiceName, a.FaviconMMH3, a.StatusCode, a.ContentLength, a.PageTitle, string(auth), a.Method, string(params), string(extra), familyValue, address).Scan(&id)
	if err != nil {
		return 0, err
	}
	for _, relation := range []struct {
		table, column string
		values        []string
	}{
		{"asset_bound_domains", "domain", a.BoundDomains}, {"asset_records", "value", a.RecordValue}, {"asset_technologies", "technology", a.Technologies},
	} {
		if _, err := s.tx.Exec(`DELETE FROM `+relation.table+` WHERE asset_id=?1`, id); err != nil {
			return 0, err
		}
		for position, value := range relation.values {
			if _, err := s.tx.Exec(`INSERT INTO `+relation.table+`(asset_id,position,`+relation.column+`) VALUES (?1,?2,?3)`, id, position, value); err != nil {
				return 0, err
			}
		}
	}
	if _, err := s.tx.Exec(`DELETE FROM asset_open_ports WHERE asset_id=?1`, id); err != nil {
		return 0, err
	}
	for position, port := range a.OpenPorts {
		portNumber := assetPortNumber(port["port"])
		if portNumber < 1 || portNumber > 65535 {
			return 0, fmt.Errorf("port must be between 1 and 65535")
		}
		service, _ := port["service"].(string)
		payload, err := json.Marshal(port)
		if err != nil {
			return 0, err
		}
		if _, err := s.tx.Exec(`INSERT INTO asset_open_ports(asset_id,position,port,service,extra) VALUES (?1,?2,?3,?4,?5)`, id, position, portNumber, service, string(payload)); err != nil {
			return 0, err
		}
	}
	if taskID > 0 {
		res, err := s.tx.Exec(`INSERT INTO task_asset_links(task_id,asset_id) SELECT id,?2 FROM tasks WHERE id=?1 AND deleted_at IS NULL ON CONFLICT(task_id,asset_id) DO NOTHING`, taskID, id)
		if err != nil {
			return 0, err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		if count == 0 {
			var exists bool
			if err := s.tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM tasks WHERE id=?1 AND deleted_at IS NULL)`, taskID).Scan(&exists); err != nil {
				return 0, err
			}
			if !exists {
				return 0, ErrTaskAssetTaskNotFound
			}
		}
	}
	return id, nil
}

func mergeAssetStrings(current, incoming []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(current)+len(incoming))
	for _, items := range [][]string{current, incoming} {
		for _, item := range items {
			if !seen[item] {
				seen[item] = true
				result = append(result, item)
			}
		}
	}
	return result
}

func nonNilAssetObjects(items []map[string]any) []map[string]any {
	if items == nil {
		return []map[string]any{}
	}
	return items
}

func mergeAssetObjects(current, incoming []map[string]any, params bool) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(current)+len(incoming))
	positions := map[string]int{}
	serialized := map[string]string{}
	for _, items := range [][]map[string]any{current, incoming} {
		for _, item := range items {
			raw, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			key := string(raw)
			if params {
				key = fmt.Sprint(item["location"]) + "\x00" + fmt.Sprint(item["name"])
			}
			if position, exists := positions[key]; exists {
				if params && string(raw) > serialized[key] {
					result[position] = item
					serialized[key] = string(raw)
				}
				continue
			}
			positions[key] = len(result)
			serialized[key] = string(raw)
			result = append(result, item)
		}
	}
	return result, nil
}

func assetPortNumber(value any) int { n, _ := strconv.Atoi(fmt.Sprint(value)); return n }

func mergeAssetPorts(current, incoming []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(current)+len(incoming))
	positions := map[int]int{}
	for _, items := range [][]map[string]any{current, incoming} {
		for _, item := range items {
			port := assetPortNumber(item["port"])
			if position, exists := positions[port]; exists {
				oldService, _ := result[position]["service"].(string)
				service, _ := item["service"].(string)
				if oldService == "" && service != "" {
					result[position] = item
				}
				continue
			}
			positions[port] = len(result)
			result = append(result, item)
		}
	}
	return result
}
