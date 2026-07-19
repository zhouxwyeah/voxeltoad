import { useEffect, useState } from "react";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Field } from "../components/ui/field";
import { Select } from "../components/ui/select";
import { Skeleton } from "../components/ui/skeleton";
import { Textarea } from "../components/ui/textarea";
import { listModels, playgroundChat } from "../lib/api";
import type { Model, PlaygroundResult } from "../lib/types";
import { formatDurationCompact, formatTokens } from "../lib/format";

// Connectivity playground: fire a tiny completion through the gateway's full
// routing chain (route → provider → credential → adapter → upstream) to
// validate config without pointing an external agent at the gateway. Upstream
// errors are shown verbatim — diagnosis is the whole point.
export function Playground() {
  const [models, setModels] = useState<Model[] | null>(null);
  const [model, setModel] = useState("");
  const [prompt, setPrompt] = useState("你好，用一句话介绍你自己。");
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState<PlaygroundResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    listModels()
      .then((ms) => {
        setModels(ms);
        if (ms.length > 0) setModel((cur) => cur || ms[0].alias);
      })
      .catch((e) => setError(String((e as Error)?.message ?? e)));
  }, []);

  async function onSend() {
    if (!model || !prompt.trim() || pending) return;
    setPending(true);
    setResult(null);
    setError(null);
    try {
      setResult(await playgroundChat(model, prompt.trim()));
    } catch (e) {
      setError(String((e as Error)?.message ?? e));
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
      <div>
        <h1 className="text-xl font-semibold text-foreground">连通性测试</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          通过网关完整链路（路由 → 供应商 → 凭证 → 上游）发一条小请求，验证配置是否可用；
          失败时会原样展示上游错误。该请求不会计入请求日志。
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>发送测试请求</CardTitle>
          <CardDescription>最多生成 512 tokens，成本可忽略。</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {models === null ? (
            <Skeleton className="h-9" />
          ) : models.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              还没有可用模型，请先在「模型」页创建。
            </p>
          ) : (
            <Field label="模型别名" required>
              <Select value={model} onChange={(e) => setModel(e.target.value)}>
                {models.map((m) => (
                  <option key={m.alias} value={m.alias}>
                    {m.alias}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          <Field label="Prompt" required>
            <Textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={3}
              placeholder="输入一条简短的测试消息"
            />
          </Field>
          <div className="flex justify-end">
            <Button
              variant="primary"
              onClick={onSend}
              disabled={!model || !prompt.trim() || pending}
            >
              {pending ? "请求中…" : "发送"}
            </Button>
          </div>
        </CardContent>
      </Card>

      {error && (
        <div role="alert" className="rounded-lg border border-destructive/30 bg-destructive/10 p-4">
          <p className="mb-1 text-sm font-medium text-destructive">调用失败（错误来自网关/上游，原样展示）</p>
          <pre className="whitespace-pre-wrap break-all font-mono text-xs text-destructive">{error}</pre>
        </div>
      )}

      {result && (
        <Card>
          <CardHeader>
            <CardTitle>响应</CardTitle>
            <CardDescription>
              经由供应商「{result.provider}」· 上游模型 {result.model_resolved} · 耗时{" "}
              {formatDurationCompact(result.latency_ms)}
              {result.fallback && " · 发生了候选回退"}
              {result.usage &&
                ` · tokens ${formatTokens(result.usage.total_tokens)}（${formatTokens(result.usage.prompt_tokens)}/${formatTokens(result.usage.completion_tokens)}）`}
              {result.finish_reason &&
                ` · 结束原因 ${result.finish_reason}${result.finish_reason === "length" ? "（输出达上限）" : ""}`}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {result.content ? (
              <p className="whitespace-pre-wrap text-sm text-foreground">{result.content}</p>
            ) : result.reasoning_content ? (
              <div className="flex flex-col gap-2">
                <p className="text-sm text-muted-foreground">
                  正文为空：思考型模型把输出预算用在了思考过程，未产出正文（链路本身正常）。
                  模型思考内容：
                </p>
                <pre className="whitespace-pre-wrap break-all rounded-md bg-muted/40 p-3 font-mono text-xs text-muted-foreground">
                  {result.reasoning_content}
                </pre>
              </div>
            ) : (
              <p className="whitespace-pre-wrap text-sm text-foreground">（空响应）</p>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
