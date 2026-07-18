import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { ConfirmModal } from "../components/ui/confirm-modal";
import { Field } from "../components/ui/field";
import { Input } from "../components/ui/input";
import { Modal } from "../components/ui/modal";
import { Select } from "../components/ui/select";
import { Skeleton } from "../components/ui/skeleton";
import { getAPIKey, getSettings, rotateAPIKey, updateSettings } from "../lib/api";
import type { APIKeyView } from "../lib/types";

// Settings page over /api/v1/settings. Two groups with honestly-labelled
// application semantics: the bootstrap gateway section (listen addr + session
// headers) only lands on restart, while the trace capture knobs hot-apply on
// save via the dispatcher rebuild.
export function Settings() {
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [addr, setAddr] = useState("");
  const [sessionHeaders, setSessionHeaders] = useState("");
  const [capture, setCapture] = useState("on");
  const [maxBodyKB, setMaxBodyKB] = useState("256");
  const [retentionDays, setRetentionDays] = useState("30");

  useEffect(() => {
    getSettings()
      .then((s) => {
        setAddr(s.gateway.addr);
        setSessionHeaders((s.gateway.session_headers ?? []).join(", "));
        setCapture(s.trace.capture_payload_enabled ? "on" : "off");
        setMaxBodyKB(String(s.trace.max_body_kb));
        setRetentionDays(String(s.trace.retention_days));
        setLoadError(null);
      })
      .catch((e) => setLoadError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  }, []);

  const maxBodyN = Number(maxBodyKB);
  const retentionN = Number(retentionDays);
  const valid =
    addr.trim() !== "" &&
    Number.isInteger(maxBodyN) &&
    maxBodyN >= 0 &&
    Number.isInteger(retentionN) &&
    retentionN >= 0;

  async function onSave() {
    if (!valid || pending) return;
    setPending(true);
    try {
      const res = await updateSettings({
        gateway: {
          addr: addr.trim(),
          session_headers: sessionHeaders
            .split(",")
            .map((h) => h.trim())
            .filter(Boolean),
        },
        trace: {
          capture_payload_enabled: capture === "on",
          max_body_kb: maxBodyN,
          retention_days: retentionN,
        },
      });
      toast.success("设置已保存。");
      if (res.warning) toast.warning(res.warning);
    } catch (e) {
      toast.error(String((e as Error)?.message ?? e));
    } finally {
      setPending(false);
    }
  }

  if (loading) {
    return (
      <div className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
        <Skeleton className="h-8 w-40" />
        <Skeleton className="h-48" />
        <Skeleton className="h-48" />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
      <div>
        <h1 className="text-xl font-semibold text-foreground">设置</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          网关级参数。供应商 / 模型 / 路由请在各自页面维护。
        </p>
      </div>

      {loadError && (
        <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {loadError}
        </p>
      )}

      <Card>
        <CardHeader>
          <CardTitle>网关监听</CardTitle>
          <CardDescription>保存后写入配置文件，重启应用后生效。</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <Field
            label="监听地址"
            required
            hint="数据面与本地 API 的绑定地址；各 Agent 的 base_url 需指向这里"
          >
            <Input value={addr} onChange={(e) => setAddr(e.target.value)} placeholder="127.0.0.1:8787" />
          </Field>
          <Field
            label="会话头"
            hint="逗号分隔；按优先级依次从这些请求头提取会话 ID（用于会话亲和路由与 Trace 聚合）"
          >
            <Input
              value={sessionHeaders}
              onChange={(e) => setSessionHeaders(e.target.value)}
              placeholder="X-Voxeltoad-Session"
            />
          </Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Trace 采集</CardTitle>
          <CardDescription>保存即生效，无需重启。</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <Field label="报文采集" hint="开启后记录每个请求的完整 messages 与原始报文（Trace 查看器的数据来源）">
            <Select value={capture} onChange={(e) => setCapture(e.target.value)}>
              <option value="on">开启</option>
              <option value="off">关闭</option>
            </Select>
          </Field>
          <div className="grid grid-cols-2 gap-4">
            <Field label="报文上限" suffix="KB" hint="单层报文超过该大小将截断；0 为不限">
              <Input
                type="number"
                min={0}
                value={maxBodyKB}
                onChange={(e) => setMaxBodyKB(e.target.value)}
              />
            </Field>
            <Field label="留存天数" suffix="天" hint="超期的请求日志与 Trace 会被自动清理；0 使用默认 30 天">
              <Input
                type="number"
                min={0}
                value={retentionDays}
                onChange={(e) => setRetentionDays(e.target.value)}
              />
            </Field>
          </div>
        </CardContent>
      </Card>

      <div className="flex justify-end">
        <Button variant="primary" onClick={onSave} disabled={!valid || pending}>
          {pending ? "保存中…" : "保存设置"}
        </Button>
      </div>

      <APIKeyCard />
    </div>
  );
}

// API 密钥卡片：展示当前密钥（明文已知时）、复制、轮换。轮换后旧密钥立即
// 失效，新明文仅展示一次 —— 与菜单栏「复制 API key」共享同一份内存状态。
function APIKeyCard() {
  const [view, setView] = useState<APIKeyView | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [rotatedKey, setRotatedKey] = useState<string | null>(null);

  useEffect(() => {
    getAPIKey()
      .then(setView)
      .catch((e) => toast.error(String((e as Error)?.message ?? e)));
  }, []);

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast.success("已复制到剪贴板。");
    } catch {
      toast.error("复制失败，请手动选择复制。");
    }
  }

  async function onRotate() {
    try {
      const res = await rotateAPIKey();
      setView(res);
      setRotatedKey(res.key ?? null);
      if (res.warning) toast.warning(res.warning);
    } catch (e) {
      toast.error(String((e as Error)?.message ?? e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>API 密钥</CardTitle>
        <CardDescription>
          各 Agent 调用网关时使用的 Bearer 密钥（本地单用户，仅一把默认密钥）。
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {view === null ? (
          <Skeleton className="h-9" />
        ) : view.plaintext_known && view.key ? (
          <div className="flex items-center gap-2">
            <code className="flex-1 truncate rounded-md border border-border bg-muted/40 px-3 py-2 font-mono text-sm text-foreground">
              {view.key}
            </code>
            <Button variant="outline" size="sm" onClick={() => copy(view.key!)}>
              复制
            </Button>
          </div>
        ) : (
          <p className="rounded-md bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
            当前密钥是轮换后重启的，明文已不可恢复。可复制环境变量 GATEWAY_DESKTOP_KEY
            指定的新密钥，或再次轮换（旧密钥将失效）。
          </p>
        )}
        <div className="flex items-center justify-between gap-3">
          <p className="text-xs text-muted-foreground">
            轮换会立即作废旧密钥，需要同步更新所有 Agent 的 Authorization 配置。
          </p>
          <Button variant="outline" size="sm" onClick={() => setConfirmOpen(true)}>
            轮换密钥
          </Button>
        </div>
      </CardContent>

      <ConfirmModal
        open={confirmOpen}
        onCancel={() => setConfirmOpen(false)}
        onConfirm={async () => {
          setConfirmOpen(false);
          await onRotate();
        }}
        title="轮换 API 密钥"
        message="轮换后旧密钥立即失效，所有使用旧密钥的 Agent 都会调用失败。确定继续？"
        confirmLabel="确认轮换"
      />

      <Modal
        open={rotatedKey !== null}
        onClose={() => setRotatedKey(null)}
        title="新密钥（仅展示一次）"
        size="md"
        footer={
          <Button variant="primary" onClick={() => rotatedKey && copy(rotatedKey)}>
            复制并关闭
          </Button>
        }
      >
        <div className="flex flex-col gap-3">
          <code className="break-all rounded-md border border-border bg-muted/40 px-3 py-2 font-mono text-sm text-foreground">
            {rotatedKey}
          </code>
          <p className="text-sm text-warning">
            请立即保存此密钥并更新各 Agent 的 Authorization 配置；关闭后将无法再次查看明文。
          </p>
        </div>
      </Modal>
    </Card>
  );
}
