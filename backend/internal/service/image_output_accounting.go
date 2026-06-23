package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/tidwall/gjson"
)

type openAIImageOutputCounter struct {
	seen          map[string]struct{}
	seenSizes     map[string]string
	seenQualities map[string]string
	seenOrder     []string
	dataSizes     []string
	dataQualities []string
	topQuality    string
	count         int
	maxDataCount  int
}

func newOpenAIImageOutputCounter() *openAIImageOutputCounter {
	return &openAIImageOutputCounter{
		seen:          make(map[string]struct{}),
		seenSizes:     make(map[string]string),
		seenQualities: make(map[string]string),
	}
}

func (c *openAIImageOutputCounter) Count() int {
	if c == nil {
		return 0
	}
	if c.maxDataCount > c.count {
		return c.maxDataCount
	}
	return c.count
}

func (c *openAIImageOutputCounter) Sizes() []string {
	if c == nil {
		return nil
	}
	sizes := make([]string, 0, len(c.seenOrder)+len(c.dataSizes))
	for _, key := range c.seenOrder {
		if size := strings.TrimSpace(c.seenSizes[key]); size != "" {
			sizes = append(sizes, size)
		}
	}
	if len(sizes) == 0 && len(c.dataSizes) > 0 {
		sizes = append(sizes, c.dataSizes...)
	}
	if len(sizes) == 0 {
		return nil
	}
	return sizes
}

// Quality 返回响应中采集到的首个非空 quality（low/medium/high）。
// 优先级：逐个图片项 quality -> data 数组项 quality -> tools[].quality 回显。
func (c *openAIImageOutputCounter) Quality() string {
	if c == nil {
		return ""
	}
	for _, key := range c.seenOrder {
		if quality := strings.TrimSpace(c.seenQualities[key]); quality != "" {
			return quality
		}
	}
	for _, quality := range c.dataQualities {
		if quality = strings.TrimSpace(quality); quality != "" {
			return quality
		}
	}
	return strings.TrimSpace(c.topQuality)
}

func (c *openAIImageOutputCounter) AddJSONResponse(body []byte) {
	if c == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return
	}
	c.addDataArray(gjson.GetBytes(body, "data"))
	c.addOutputArray(gjson.GetBytes(body, "output"))
	c.addOutputArray(gjson.GetBytes(body, "response.output"))
	c.addToolsQuality(gjson.GetBytes(body, "tools"))
	c.addToolsQuality(gjson.GetBytes(body, "response.tools"))
}

func (c *openAIImageOutputCounter) AddSSEData(data []byte) {
	if c == nil || len(data) == 0 || strings.TrimSpace(string(data)) == "[DONE]" || !gjson.ValidBytes(data) {
		return
	}
	root := gjson.ParseBytes(data)
	c.addDataArray(root.Get("data"))
	eventType := strings.TrimSpace(root.Get("type").String())
	switch eventType {
	case "response.created", "response.in_progress":
		c.addToolsQuality(root.Get("response.tools"))
	case "response.output_item.done":
		c.addImageOutputItem(root.Get("item"))
	case "response.completed", "response.done":
		c.addOutputArray(root.Get("response.output"))
		c.addToolsQuality(root.Get("response.tools"))
	case "image_generation.completed":
		if item := root.Get("item"); item.Exists() {
			c.addImageOutputItem(item)
			return
		}
		if output := root.Get("output"); output.Exists() {
			c.addImageOutputItem(output)
			return
		}
		c.addImageOutputItem(root)
	}
}

func (c *openAIImageOutputCounter) AddSSEBody(body string) {
	if c == nil || strings.TrimSpace(body) == "" {
		return
	}
	forEachOpenAISSEDataPayload(body, c.AddSSEData)
}

func (c *openAIImageOutputCounter) addDataArray(data gjson.Result) {
	if !data.IsArray() {
		return
	}
	items := data.Array()
	count := len(items)
	if count > c.maxDataCount {
		c.maxDataCount = count
	}
	sizes := make([]string, 0, len(items))
	qualities := make([]string, 0, len(items))
	for _, item := range items {
		if size := strings.TrimSpace(item.Get("size").String()); size != "" {
			sizes = append(sizes, size)
		}
		if quality := strings.TrimSpace(item.Get("quality").String()); quality != "" {
			qualities = append(qualities, quality)
		}
	}
	if len(sizes) > 0 {
		c.dataSizes = sizes
	}
	if len(qualities) > 0 {
		c.dataQualities = qualities
	}
}

// addToolsQuality 从 tools 数组中提取 image_generation 工具回显的 quality。
func (c *openAIImageOutputCounter) addToolsQuality(tools gjson.Result) {
	if c.topQuality != "" || !tools.IsArray() {
		return
	}
	tools.ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("type").String()) != "image_generation" {
			return true
		}
		if quality := strings.TrimSpace(item.Get("quality").String()); quality != "" {
			c.topQuality = quality
			return false
		}
		return true
	})
}

func (c *openAIImageOutputCounter) addOutputArray(output gjson.Result) {
	if !output.IsArray() {
		return
	}
	output.ForEach(func(_, item gjson.Result) bool {
		c.addImageOutputItem(item)
		return true
	})
}

func (c *openAIImageOutputCounter) addImageOutputItem(item gjson.Result) {
	if !item.Exists() || !item.IsObject() {
		return
	}
	itemType := strings.TrimSpace(item.Get("type").String())
	if itemType != "" && itemType != "image_generation_call" && itemType != "image_generation.completed" {
		return
	}
	if strings.Contains(strings.ToLower(item.Raw), "partial_image") {
		return
	}
	result := strings.TrimSpace(item.Get("result").String())
	if result == "" {
		result = strings.TrimSpace(item.Get("b64_json").String())
	}
	if result == "" {
		result = strings.TrimSpace(item.Get("url").String())
	}
	if result == "" && itemType != "image_generation.completed" {
		return
	}
	key := strings.TrimSpace(item.Get("id").String())
	if key == "" {
		key = strings.TrimSpace(item.Get("call_id").String())
	}
	if key == "" {
		key = hashOpenAIImageOutputResult(result)
	}
	if key == "" {
		return
	}
	size := strings.TrimSpace(item.Get("size").String())
	quality := strings.TrimSpace(item.Get("quality").String())
	if _, exists := c.seen[key]; exists {
		if size != "" && strings.TrimSpace(c.seenSizes[key]) == "" {
			c.seenSizes[key] = size
		}
		if quality != "" && strings.TrimSpace(c.seenQualities[key]) == "" {
			c.seenQualities[key] = quality
		}
		return
	}
	c.seen[key] = struct{}{}
	c.seenOrder = append(c.seenOrder, key)
	if size != "" {
		c.seenSizes[key] = size
	}
	if quality != "" {
		c.seenQualities[key] = quality
	}
	c.count++
}

func hashOpenAIImageOutputResult(result string) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(result))
	return hex.EncodeToString(sum[:])
}

func countOpenAIResponseImageOutputsFromJSONBytes(body []byte) int {
	counter := newOpenAIImageOutputCounter()
	counter.AddJSONResponse(body)
	return counter.Count()
}

func collectOpenAIResponseImageOutputSizesFromJSONBytes(body []byte) []string {
	counter := newOpenAIImageOutputCounter()
	counter.AddJSONResponse(body)
	return counter.Sizes()
}

func collectOpenAIResponseImageOutputQualityFromJSONBytes(body []byte) string {
	counter := newOpenAIImageOutputCounter()
	counter.AddJSONResponse(body)
	return counter.Quality()
}

func countOpenAIImageOutputsFromSSEBody(body string) int {
	counter := newOpenAIImageOutputCounter()
	counter.AddSSEBody(body)
	return counter.Count()
}

func collectOpenAIImageOutputSizesFromSSEBody(body string) []string {
	counter := newOpenAIImageOutputCounter()
	counter.AddSSEBody(body)
	return counter.Sizes()
}

func collectOpenAIImageOutputQualityFromSSEBody(body string) string {
	counter := newOpenAIImageOutputCounter()
	counter.AddSSEBody(body)
	return counter.Quality()
}
