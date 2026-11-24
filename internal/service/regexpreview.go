// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import "regexp"

// RuleRegexPreviewInput 中，Replacement 为 nil 时只匹配，空字符串用于删除匹配内容。
type RuleRegexPreviewInput struct {
	Pattern     string  `json:"pattern"`
	Sample      string  `json:"sample"`
	Replacement *string `json:"replacement"`
	// CaseSensitive 只用于条件匹配。改写动作默认区分大小写，可在表达式中用 (?i) 切换。
	CaseSensitive bool `json:"caseSensitive"`
}

// RuleRegexPreviewResult 用 Matched 区分未命中和替换后得到空文本。
type RuleRegexPreviewResult struct {
	Matched     bool   `json:"matched"`
	Output      string `json:"output"`
	SyntaxError string `json:"syntaxError,omitempty"`
	TooLarge    bool   `json:"tooLarge,omitempty"`
}

// PreviewRuleRegex 将语法错误作为预览结果返回，供编辑器即时展示。
func PreviewRuleRegex(input RuleRegexPreviewInput) RuleRegexPreviewResult {
	replacement := input.Replacement
	pattern := input.Pattern
	if replacement != nil && pattern == "" {
		return RuleRegexPreviewResult{}
	}
	if replacement == nil && !input.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return RuleRegexPreviewResult{SyntaxError: err.Error()}
	}
	// 大量短匹配会反复展开替换值，因此样本和替换值共用 8 KiB 的预算。
	if replacement != nil && len(input.Sample)+len(*replacement) > 8<<10 {
		return RuleRegexPreviewResult{TooLarge: true}
	}
	matched := re.MatchString(input.Sample)
	if !matched || replacement == nil {
		return RuleRegexPreviewResult{Matched: matched}
	}

	output := re.ReplaceAllString(input.Sample, *replacement)
	if len(output) > 64<<10 {
		return RuleRegexPreviewResult{TooLarge: true}
	}
	return RuleRegexPreviewResult{Matched: true, Output: output}
}
