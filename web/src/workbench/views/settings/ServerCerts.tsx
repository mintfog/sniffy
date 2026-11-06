import {
  useEffect,
  useId,
  useState,
  type Dispatch,
  type SetStateAction,
} from 'react'
import { useTranslation } from 'react-i18next'
import { FileKey2, Plus, Trash2 } from 'lucide-react'
import { Bridge, type ServerCert } from '@/lib/bridge'
import { Button } from '../../ui/controls'
import { ConfirmDialog } from '../../ui/ConfirmDialog'

function PemField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  placeholder: string
}) {
  const id = useId()
  return (
    <div className="min-w-0">
      <label htmlFor={id} className="settings-field-label mb-1.5 block">
        {label}
      </label>
      <textarea
        id={id}
        spellCheck={false}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        rows={5}
        className="w-full resize-y rounded-wb border border-line bg-inset px-2 py-1.5 font-mono text-[11px] leading-relaxed text-fg outline-none transition-colors placeholder:text-fg-faint focus:border-accent focus:bg-surface"
      />
    </div>
  )
}

export interface ServerCertForm {
  certPem: string
  keyPem: string
  importing: boolean
  error: string
  /** 成功导入次数；导入可能在组件卸载期间完成，重新挂载的实例据此刷新列表。 */
  imported: number
}

/**
 * 导入服务端证书：应对证书固定场景——用真实证书+私钥替代 MITM 现签的伪造证书。
 * 设置页筛选分类或搜索时会卸载本组件，草稿与导入状态由页面层持有。
 */
export function ServerCerts({
  form,
  setForm,
}: {
  form: ServerCertForm
  setForm: Dispatch<SetStateAction<ServerCertForm>>
}) {
  const { t } = useTranslation()
  const [certs, setCerts] = useState<ServerCert[]>([])
  const { certPem, keyPem, importing, error } = form
  const [confirmDelete, setConfirmDelete] = useState<ServerCert | null>(null)

  const refresh = () => {
    Bridge.getServerCerts()
      .then((list) => setCerts(list ?? []))
      .catch(() => {
        /* 未连接后端：保持空 */
      })
  }
  useEffect(refresh, [form.imported])

  const doImport = async () => {
    if (!certPem.trim() || !keyPem.trim()) {
      setForm((f) => ({ ...f, error: t('certs.serverCert.fieldsRequired') }))
      return
    }
    setForm((f) => ({ ...f, importing: true, error: '' }))
    try {
      await Bridge.importServerCert(certPem, keyPem)
      // 等待期间用户可能已改写草稿，只清空与本次提交一致的内容。
      setForm((f) => {
        const unchanged = f.certPem === certPem && f.keyPem === keyPem
        return {
          ...f,
          certPem: unchanged ? '' : f.certPem,
          keyPem: unchanged ? '' : f.keyPem,
          importing: false,
          imported: f.imported + 1,
        }
      })
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e)
      setForm((f) => ({ ...f, importing: false, error: message }))
    }
  }

  const doDelete = (id: string) => {
    Bridge.deleteServerCert(id)
      .catch(() => {})
      .finally(() => {
        setConfirmDelete(null)
        refresh()
      })
  }

  return (
    <div className="flex flex-col gap-4 py-3">
      {certs.length > 0 ? (
        <div className="flex flex-col divide-y divide-line border-y border-line">
          {certs.map((c) => {
            const expired = !!c.notAfter && new Date(c.notAfter).getTime() < Date.now()
            return (
              <div key={c.id} className="flex items-center gap-3 py-2">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate font-mono text-[13px] text-fg">{(c.hosts ?? []).join(', ') || '—'}</span>
                    {expired && (
                      <span className="rounded-wb bg-warn/12 px-1.5 py-0.5 text-2xs font-medium text-warn">
                        {t('certs.serverCert.expired')}
                      </span>
                    )}
                  </div>
                  <div className="truncate text-2xs text-fg-faint">
                    {c.subject || '—'}
                    {c.notAfter && ` · ${t('certs.serverCert.expires')} ${c.notAfter.slice(0, 10)}`}
                  </div>
                </div>
                <Button
                  variant="danger"
                  size="sm"
                  icon={<Trash2 className="h-3.5 w-3.5" />}
                  onClick={() => setConfirmDelete(c)}
                >
                  {t('certs.serverCert.deleteBtn')}
                </Button>
              </div>
            )
          })}
        </div>
      ) : (
        <div className="flex items-center gap-2 text-[12px] text-fg-faint">
          <FileKey2 className="h-4 w-4" />
          {t('certs.serverCert.empty')}
        </div>
      )}

      <div className="grid grid-cols-2 gap-3">
        <PemField
          label={t('certs.serverCert.certLabel')}
          value={certPem}
          onChange={(v) => setForm((f) => ({ ...f, certPem: v }))}
          placeholder="-----BEGIN CERTIFICATE-----"
        />
        <PemField
          label={t('certs.serverCert.keyLabel')}
          value={keyPem}
          onChange={(v) => setForm((f) => ({ ...f, keyPem: v }))}
          placeholder="-----BEGIN PRIVATE KEY-----"
        />
      </div>
      {error && (
        <div role="alert" className="text-[12px] leading-relaxed text-danger">
          {error}
        </div>
      )}
      <div className="flex justify-end">
        <Button variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={doImport} disabled={importing}>
          {importing ? t('certs.serverCert.importing') : t('certs.serverCert.importBtn')}
        </Button>
      </div>

      {confirmDelete && (
        <ConfirmDialog
          title={t('certs.serverCert.deleteTitle')}
          message={t('certs.serverCert.deleteConfirm', { host: (confirmDelete.hosts ?? []).join(', ') })}
          confirmLabel={t('certs.serverCert.deleteBtn')}
          cancelLabel={t('certs.cancel')}
          tone="danger"
          onConfirm={() => doDelete(confirmDelete.id)}
          onClose={() => setConfirmDelete(null)}
        />
      )}
    </div>
  )
}
