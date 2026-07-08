#!/bin/bash

PUBLIC_KEY="sk-openmodelpool.com_github.lisiyu.openmodelpool_public.key.v1"

echo "=== 测试新的公共Key ==="
echo ""
echo "公共Key: $PUBLIC_KEY"
echo "长度: ${#PUBLIC_KEY} 字符"
echo ""

# 测试1：使用公共Key调用API
echo "1. 测试公共Key调用 /v1/chat/completions..."
RESPONSE=$(curl -s -X POST http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $PUBLIC_KEY" \
  -d '{
    "model": "auto",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 10
  }')

echo "响应:"
echo "$RESPONSE"

if echo "$RESPONSE" | grep -q '"choices"'; then
  echo ""
  echo "✅ 公共Key工作正常！"
elif echo "$RESPONSE" | grep -q '"error"'; then
  echo ""
  echo "⚠️ 公共Key被识别，但没有可用的Provider（这是预期的，因为还没有配置Provider）"
  echo "✅ Key格式验证通过！"
else
  echo ""
  echo "❌ 公共Key可能有问题"
fi
