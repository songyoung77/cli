// Copyright (c) 2026 Lark Technologies Pte. Ltd.
// SPDX-License-Identifier: MIT

package base

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/larksuite/cli/internal/output"
	"github.com/larksuite/cli/shortcuts/common"
)

const (
	uploadAttachConcurrency = 5
)

var BaseFormSubmit = common.Shortcut{
	Service:     "base",
	Command:     "+form-submit",
	Description: "Submit a form (fill and submit form data)",
	Risk:        "write",
	Scopes:      []string{"base:form:update", "docs:document.media:upload"},
	AuthTypes:   authTypes(),
	HasFormat:   true,
	Flags: []common.Flag{
		{Name: "share-token", Desc: "表单分享 Token（必填），从表单分享链接中提取", Required: true},
		{Name: "base-token", Desc: "Base token（--json 包含 attachments 时必填，用于上传附件到 Base Drive Media）"},
		{Name: "json", Desc: `JSON 对象，包含 "fields"（普通字段值）和 "attachments"（附件文件路径）。示例：'{"fields":{"评分":5,"评价":"好"},"attachments":{"附件":["./a.pdf","./b.png"]}}'`, Required: true},
	},
	Tips: []string{
		`示例（无附件）：--share-token shrcnkbanhog5cHymqg2VHp5Tth --json '{"fields":{"服务评分":5,"评价内容":"服务态度好"}}'`,
		`示例（带附件）：--share-token shrcnkbanhog5cHymqg2VHp5Tth --base-token basXXX --json '{"fields":{"服务评分":5},"attachments":{"附件":["./report.pdf"]}}'`,
		`"fields" 中的单元格值写法与 lark-base-cell-value.md 对齐；"attachments" 将字段名映射到本地文件路径数组，CLI 自动并行上传后合并写入。`,
	},
	Validate: func(ctx context.Context, runtime *common.RuntimeContext) error {
		return validateFormSubmit(runtime)
	},
	DryRun: dryRunFormSubmit,
	Execute: func(ctx context.Context, runtime *common.RuntimeContext) error {
		return executeFormSubmit(runtime)
	},
}

func validateFormSubmit(runtime *common.RuntimeContext) error {
	// 校验 --json 结构：提取 "fields" 和 "attachments"
	pc := newParseCtx(runtime)
	raw, err := parseJSONObject(pc, runtime.Str("json"), "json")
	if err != nil {
		return err
	}

	fields, _ := raw["fields"].(map[string]interface{})
	attachments, hasAttachments := raw["attachments"]

	if !hasAttachments && fields == nil {
		return common.FlagErrorf("--json must contain at least \"fields\" or \"attachments\"")
	}

	if hasAttachments {
		// 有附件时 --base-token 必填（上传附件到 Base Drive Media 需要）
		if runtime.Str("base-token") == "" {
			return common.FlagErrorf("--base-token is required when --json contains \"attachments\"")
		}

		attMap, ok := attachments.(map[string]interface{})
		if !ok {
			return common.FlagErrorf("--json.attachments must be a JSON object mapping field names to file path arrays")
		}
		for fieldName, value := range attMap {
			paths, ok := value.([]interface{})
			if !ok {
				return common.FlagErrorf("--json.attachments.%q must be a file path array, got %T", fieldName, value)
			}
			for i, item := range paths {
				if _, ok := item.(string); !ok {
					return common.FlagErrorf("--json.attachments.%q[%d] must be a file path string, got %T", fieldName, i, item)
				}
			}
			if len(paths) == 0 {
				return common.FlagErrorf("--json.attachments.%q must not be empty; remove it or provide at least one file path", fieldName)
			}
		}
	}

	return nil
}

// parseFormSubmitJSON 将 --json 解析为字段和附件映射。
func parseFormSubmitJSON(runtime *common.RuntimeContext) (map[string]interface{}, map[string][]string, error) {
	pc := newParseCtx(runtime)
	raw, err := parseJSONObject(pc, runtime.Str("json"), "json")
	if err != nil {
		return nil, nil, err
	}

	fields, _ := raw["fields"].(map[string]interface{})
	if fields == nil {
		fields = make(map[string]interface{})
	}

	var attMap map[string][]string
	if attachments, ok := raw["attachments"]; ok {
		attObj, ok := attachments.(map[string]interface{})
		if ok && len(attObj) > 0 {
			attMap = make(map[string][]string, len(attObj))
			for fieldName, value := range attObj {
				paths, ok := value.([]interface{})
				if !ok {
					continue
				}
				filePaths := make([]string, 0, len(paths))
				for _, item := range paths {
					if s, ok := item.(string); ok {
						filePaths = append(filePaths, s)
					}
				}
				if len(filePaths) > 0 {
					attMap[fieldName] = filePaths
				}
			}
		}
	}

	return fields, attMap, nil
}

func dryRunFormSubmit(_ context.Context, runtime *common.RuntimeContext) *common.DryRunAPI {
	fields, attachmentMap, _ := parseFormSubmitJSON(runtime)

	if len(attachmentMap) > 0 {
		dry := common.NewDryRunAPI().
			Desc("Form submit with attachments: upload local files per field → merge with fields → submit").
			POST("/open-apis/base/v3/bases/tables/forms/submit")

		for fieldName, filePaths := range attachmentMap {
			for _, p := range filePaths {
				fileName := filepath.Base(p)
				dry = dry.POST("/open-apis/drive/v1/medias/upload_all").
					Desc(fmt.Sprintf("Upload attachment for field %q: %s", fieldName, fileName)).
					Body(map[string]interface{}{
						"file_name":   fileName,
						"parent_type": baseAttachmentParentType,
						"parent_node": "<base_token>",
						"file":        "@" + p,
						"size":        "<file_size>",
					})
			}
		}

		body := buildFormSubmitBody(runtime, fields)
		dry = dry.POST("/open-apis/base/v3/bases/tables/forms/submit").
			Body(body).
			Desc("Submit form with uploaded attachment tokens merged with fields")
		return dry
	}

	body := buildFormSubmitBody(runtime, fields)
	return common.NewDryRunAPI().
		POST("/open-apis/base/v3/bases/tables/forms/submit").
		Body(body)
}

func buildFormSubmitBody(runtime *common.RuntimeContext, content map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"share_token": runtime.Str("share-token"),
		"content":     content,
	}
}

func executeFormSubmit(runtime *common.RuntimeContext) error {
	fields, attachmentMap, err := parseFormSubmitJSON(runtime)
	if err != nil {
		return err
	}

	// 上传附件并合并到字段中
	if len(attachmentMap) > 0 {
		baseToken := runtime.Str("base-token")
		fio := runtime.FileIO()
		if fio == nil {
			return output.ErrValidation("file operations require a FileIO provider (needed for attachments in --json)")
		}

		// Step 1: 收集所有唯一路径（跨字段去重）
		allPaths := collectUniquePaths(attachmentMap)
		if len(allPaths) == 0 {
			return common.FlagErrorf("attachments in --json contains no valid file paths")
		}

		// Step 2: 前置校验所有文件，同时收集文件大小供上传使用
		sizeMap := make(map[string]int64, len(allPaths))
		for _, filePath := range allPaths {
			fileInfo, err := fio.Stat(filePath)
			if err != nil {
				return output.ErrValidation("attachment file not accessible: %s: %v", filePath, err)
			}
			if fileInfo.Size() > baseAttachmentUploadMaxFileSize {
				return output.ErrValidation("attachment file %s exceeds 2GB limit", filePath)
			}
			if !fileInfo.Mode().IsRegular() {
				return output.ErrValidation("attachment file %s is not a regular file", filePath)
			}
			sizeMap[filePath] = fileInfo.Size()
		}

		// Step 3: 并行上传，构建路径 → 附件结果映射
		fmt.Fprintf(runtime.IO().ErrOut, "Uploading %d unique attachment(s)...\n", len(allPaths))
		resultMap, err := uploadAttachmentsParallel(runtime, allPaths, baseToken, sizeMap)
		if err != nil {
			return err
		}

		// Step 4: 根据共享结果映射，按字段组装单元格
		for fieldName, filePaths := range attachmentMap {
			cell := make([]interface{}, 0, len(filePaths))
			for _, p := range filePaths {
				if att, ok := resultMap[p]; ok {
					cell = append(cell, att)
				}
			}
			fields[fieldName] = cell
		}
		fmt.Fprintf(runtime.IO().ErrOut, "Uploaded %d unique file(s) into %d field(s)\n", len(resultMap), len(attachmentMap))
	}

	body := buildFormSubmitBody(runtime, fields)
	bodyJSON, _ := json.Marshal(body)
	fmt.Fprintf(runtime.IO().ErrOut, "Final body: %s\n", bodyJSON)
	data, err := baseV3Call(runtime, "POST",
		baseV3Path("bases", "tables", "forms", "submit"),
		nil, body)
	if err != nil {
		return err
	}

	runtime.OutFormat(data, &output.Meta{Count: 1}, func(w io.Writer) {
		output.PrintTable(w, []map[string]interface{}{
			{
				"can_submit_again": data["can_submit_again"],
			},
		})
	})
	return nil
}

// collectUniquePaths 收集所有字段中的文件路径，返回去重后的有序列表。
func collectUniquePaths(attachmentMap map[string][]string) []string {
	seen := make(map[string]bool, len(attachmentMap)*4)
	var order []string
	for _, filePaths := range attachmentMap {
		for _, p := range filePaths {
			if !seen[p] {
				seen[p] = true
				order = append(order, p)
			}
		}
	}
	return order
}

// uploadAttachmentsParallel 并发上传文件，返回路径 → 附件对象的映射。
func uploadAttachmentsParallel(runtime *common.RuntimeContext, paths []string, baseToken string, sizeMap map[string]int64) (map[string]interface{}, error) {
	var (
		mu        sync.Mutex
		resultMap = make(map[string]interface{}, len(paths))
	)

	g, _ := errgroup.WithContext(context.Background())
	g.SetLimit(uploadAttachConcurrency) // 限制并发数

	for _, filePath := range paths {
		fp := filePath // 捕获循环变量
		g.Go(func() error {
			fileName := filepath.Base(fp)
			fmt.Fprintf(runtime.IO().ErrOut, "  Uploading: %s\n", fileName)

			att, err := uploadSingleAttachment(runtime, fp, fileName, baseToken, sizeMap[fp])
			if err != nil {
				return err
			}

			mu.Lock()
			resultMap[fp] = att
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return resultMap, nil
}

// uploadSingleAttachment 上传单个文件，返回附件单元格项。
// 前置条件：文件已通过校验（存在、常规文件、大小在限制内）。
func uploadSingleAttachment(runtime *common.RuntimeContext, filePath, fileName, baseToken string, fileSize int64) (interface{}, error) {
	att, err := uploadAttachmentToBase(runtime, filePath, fileName, baseToken, fileSize)
	if err != nil {
		return nil, fmt.Errorf("failed to upload attachment %s: %w", filePath, err)
	}
	return att, nil
}
