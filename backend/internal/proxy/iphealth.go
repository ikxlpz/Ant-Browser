package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ant-chrome/backend/internal/config"
)

const DefaultIPHealthURL = "https://my.ippure.com/v1/info"

// ipQueryEndpoint 备用 IP 查询接口
type ipQueryEndpoint struct {
	url    string
	parser func(body []byte) (map[string]interface{}, error)
}

// fallbackEndpoints 当 IPPure 被 Cloudflare 拦截时的备用接口列表
// 这些接口均不使用 Cloudflare，可直接通过代理访问
var fallbackEndpoints = []ipQueryEndpoint{
	{
		// ipinfo.io：无 Cloudflare，返回标准 JSON
		url: "https://ipinfo.io/json",
		parser: func(body []byte) (map[string]interface{}, error) {
			var raw map[string]interface{}
			if err := json.Unmarshal(body, &raw); err != nil {
				return nil, err
			}
			// org 格式: "AS12345 Some ISP"
			org, _ := raw["org"].(string)
			return map[string]interface{}{
				"ip":             raw["ip"],
				"country":        raw["country"],
				"region":         raw["region"],
				"city":           raw["city"],
				"asOrganization": org,
				"fraudScore":     0,
				"isResidential":  false,
				"isBroadcast":    false,
				"_source":        "ipinfo.io",
			}, nil
		},
	},
	{
		// ip-api.com：无 Cloudflare，HTTP 接口（注意：HTTPS 需付费，用 HTTP）
		url: "http://ip-api.com/json/?fields=status,message,country,regionName,city,org,query",
		parser: func(body []byte) (map[string]interface{}, error) {
			var raw map[string]interface{}
			if err := json.Unmarshal(body, &raw); err != nil {
				return nil, err
			}
			if status, _ := raw["status"].(string); status != "success" {
				msg, _ := raw["message"].(string)
				return nil, fmt.Errorf("ip-api.com 返回失败: %s", msg)
			}
			return map[string]interface{}{
				"ip":             raw["query"],
				"country":        raw["country"],
				"region":         raw["regionName"],
				"city":           raw["city"],
				"asOrganization": raw["org"],
				"fraudScore":     0,
				"isResidential":  false,
				"isBroadcast":    false,
				"_source":        "ip-api.com",
			}, nil
		},
	},
	{
		// ifconfig.me：极简接口，只返回 IP 文本
		url: "https://ifconfig.me/ip",
		parser: func(body []byte) (map[string]interface{}, error) {
			ip := strings.TrimSpace(string(body))
			if ip == "" {
				return nil, fmt.Errorf("ifconfig.me 返回空")
			}
			return map[string]interface{}{
				"ip":             ip,
				"country":        "",
				"region":         "",
				"city":           "",
				"asOrganization": "",
				"fraudScore":     0,
				"isResidential":  false,
				"isBroadcast":    false,
				"_source":        "ifconfig.me",
			}, nil
		},
	},
}

type IPHealthConfig struct {
	URL     string
	Source  string
	Parser  string
	Timeout time.Duration
}

// FetchDefaultIPHealthInfo 使用传入的检测目标查询出口 IP 健康信息。
// 返回值为第三方接口原始 JSON（map 形式），不做本地评分计算。
func FetchDefaultIPHealthInfo(
	proxyId string,
	proxies []config.BrowserProxy,
	xrayMgr *XrayManager,
	singboxMgr *SingBoxManager,
) (map[string]interface{}, error) {
	return FetchIPHealthInfo(proxyId, proxies, xrayMgr, singboxMgr, nil, config.BrowserConnectorXray, nil)
}

func FetchIPHealthInfo(
	proxyId string,
	proxies []config.BrowserProxy,
	xrayMgr *XrayManager,
	singboxMgr *SingBoxManager,
	clashMgr *ClashManager,
	connectorType string,
	cfg *IPHealthConfig,
) (map[string]interface{}, error) {
	if cfg == nil {
		cfg = &IPHealthConfig{}
	}
	targetURL := strings.TrimSpace(cfg.URL)
	if targetURL == "" {
		targetURL = DefaultIPHealthURL
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	source := resolveIPHealthSource(cfg, targetURL)
	parser := resolveIPHealthParser(cfg.Parser)
	meta := map[string]interface{}{
		"_source":    source,
		"_targetUrl": targetURL,
		"_parser":    parser,
	}
	if targetURL == "" {
		meta["error"] = "IP 健康检测目标 URL 为空"
		return meta, fmt.Errorf("IP 健康检测目标 URL 为空")
	}

	src := resolveProxyConfig("", proxies, proxyId)
	if src == "" {
		meta["error"] = "未找到代理配置"
		return meta, fmt.Errorf("未找到代理配置")
	}

	client, err := buildIPHealthHTTPClient(src, proxyId, proxies, xrayMgr, singboxMgr, clashMgr, connectorType, timeout)
	if err != nil {
		meta["error"] = err.Error()
		return meta, fmt.Errorf("创建 IP 健康检测客户端失败（source=%s）: %w", source, err)
	}

	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		meta["error"] = err.Error()
		return meta, fmt.Errorf("创建 IP 健康检测请求失败（source=%s）: %w", source, err)
	}
	if strings.Contains(strings.ToLower(targetURL), "ippure.com") {
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		req.Header.Set("Referer", "https://my.ippure.com/")
	} else {
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "AntChrome/1.0")
	}

	resp, err := client.Do(req)
	var primaryErr error
	var body []byte
	if err != nil {
		primaryErr = fmt.Errorf("调用 IP 健康检测接口失败（source=%s）: %w", source, err)
	} else {
		defer resp.Body.Close()
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			primaryErr = fmt.Errorf("读取 IP 健康检测响应失败（source=%s）: %w", source, err)
		} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			snippet := bodySnippet(body, 180)
			primaryErr = fmt.Errorf("IP 健康检测 HTTP %d（source=%s）: %s", resp.StatusCode, source, snippet)
			meta["_statusCode"] = resp.StatusCode
			if snippet != "" {
				meta["_bodySnippet"] = snippet
			}
		}
	}

	// 如果主接口失败，检查是否需要自动降级到备用接口
	isDefaultOrIPPure := strings.Contains(strings.ToLower(targetURL), "ippure.com") || targetURL == DefaultIPHealthURL
	if primaryErr != nil {
		if isDefaultOrIPPure && isCloudflareBlock(primaryErr) {
			for _, ep := range fallbackEndpoints {
				data, fallbackErr := fetchFromEndpoint(client, ep)
				if fallbackErr == nil {
					return data, nil
				}
			}
		}
		meta["error"] = primaryErr.Error()
		return meta, primaryErr
	}

	result, err := parseIPHealthBody(body, cfg.Parser)
	if err != nil {
		snippet := bodySnippet(body, 180)
		meta["error"] = err.Error()
		if snippet != "" {
			meta["_bodySnippet"] = snippet
		}
		return meta, fmt.Errorf("IP 健康检测响应解析失败（source=%s, parser=%s）: %w", source, parser, err)
	}
	result["_source"] = source
	result["_targetUrl"] = targetURL
	result["_parser"] = parser
	return result, nil
}

func parseIPHealthBody(body []byte, parser string) (map[string]interface{}, error) {
	if strings.EqualFold(strings.TrimSpace(parser), "cloudflare_trace") {
		result := map[string]interface{}{}
		for _, line := range strings.Split(string(body), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && strings.TrimSpace(key) != "" {
				result[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
		if ip := mapString(result, "ip"); ip != "" {
			result["ip"] = ip
		}
		return result, nil
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func fetchFromEndpoint(client *http.Client, ep ipQueryEndpoint) (map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, ep.url, nil)
	if err != nil {
		return nil, fmt.Errorf("构建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "curl/8.4.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 %s 失败: %w", ep.url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 响应失败: %w", ep.url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s HTTP %d: %s", ep.url, resp.StatusCode, bodySnippet(body, 120))
	}

	return ep.parser(body)
}

// isCloudflareBlock 判断错误是否为 Cloudflare 拦截（403 + Challenge 页面）
func isCloudflareBlock(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return (strings.Contains(msg, "HTTP 403") || strings.Contains(msg, "HTTP 503")) &&
		(strings.Contains(msg, "Just a moment") ||
			strings.Contains(msg, "Cloudflare") ||
			strings.Contains(msg, "DOCTYPE html") ||
			strings.Contains(msg, "<html"))
}

func mapString(data map[string]interface{}, key string) string {
	value, ok := data[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func buildIPHealthHTTPClient(
	src string,
	proxyId string,
	proxies []config.BrowserProxy,
	xrayMgr *XrayManager,
	singboxMgr *SingBoxManager,
	clashMgr *ClashManager,
	connectorType string,
	timeout time.Duration,
) (*http.Client, error) {
	return buildProxyHTTPClient(src, proxyId, proxies, xrayMgr, singboxMgr, clashMgr, connectorType, timeout)
}

func resolveIPHealthSource(cfg *IPHealthConfig, targetURL string) string {
	if cfg != nil {
		if source := strings.TrimSpace(cfg.Source); source != "" {
			return source
		}
		if parser := strings.TrimSpace(cfg.Parser); parser != "" {
			return parser
		}
	}
	if DefaultIPHealthURL != "" && strings.EqualFold(strings.TrimSpace(targetURL), DefaultIPHealthURL) {
		return "ip_health"
	}
	if parsed, err := url.Parse(strings.TrimSpace(targetURL)); err == nil {
		if host := strings.ToLower(strings.TrimSpace(parsed.Hostname())); host != "" {
			return host
		}
	}
	return "ip_health"
}

func resolveIPHealthParser(parser string) string {
	normalized := strings.TrimSpace(parser)
	if normalized == "" {
		return "json"
	}
	return normalized
}

func bodySnippet(body []byte, max int) string {
	s := strings.TrimSpace(string(body))
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
