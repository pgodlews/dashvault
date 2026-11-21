import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'

export const Route = createFileRoute('/setup')({
  component: SetupPage,
})

// ── types ─────────────────────────────────────────────────────────────────────

interface ScannedNetwork {
  ssid: string
  signal: number
  security: string
}

type ArchiveType = 'cifs' | 'nfs' | 'rclone' | 'rsync' | 'none'

// ── page ──────────────────────────────────────────────────────────────────────

function SetupPage() {
  const [step, setStep] = useState<0 | 1 | 2 | 3>(0)

  // step 0 — network
  const [networks, setNetworks] = useState<ScannedNetwork[] | null>(null)
  const [scanError, setScanError] = useState('')
  const [scanning, setScanning] = useState(false)
  const [selectedSSID, setSelectedSSID] = useState<string | null>(null)
  const [wifiPw, setWifiPw] = useState('')
  const [showWifiPw, setShowWifiPw] = useState(false)
  // The Wi-Fi network this device is already on, if any.
  const [connectedSSID, setConnectedSSID] = useState<string | null>(null)
  const [checkingNetwork, setCheckingNetwork] = useState(true)
  const [changingNetwork, setChangingNetwork] = useState(false)

  // step 1 — archive
  const [archiveType, setArchiveType] = useState<ArchiveType>('none')
  const [archiveHost, setArchiveHost] = useState('')
  const [shareName, setShareName] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showSmBPw, setShowSmbPw] = useState(false)

  // step 2 — benchmark
  const [benchLog, setBenchLog] = useState<string[]>([])
  const [benchResult, setBenchResult] = useState<BenchmarkRunResult | null>(null)
  const [benchRunning, setBenchRunning] = useState(false)
  const benchEndRef = useRef<HTMLDivElement>(null)

  // step 3 — applying
  const [logLines, setLogLines] = useState<string[]>([])
  const [applyDone, setApplyDone] = useState(false)
  const [applyError, setApplyError] = useState('')
  const logEndRef = useRef<HTMLDivElement>(null)

  const navigate = useNavigate()

  // ── network on mount ───────────────────────────────────────────────────────

  // Already on Wi-Fi: offer to keep it, and only scan if asked to change it.
  useEffect(() => {
    let cancelled = false
    fetch('/api/status')
      .then(res => (res.ok ? res.json() : null))
      .catch(() => null)
      .then((data: { wifi?: { ssid?: string } } | null) => {
        if (cancelled) return
        const ssid = data?.wifi?.ssid || null
        setConnectedSSID(ssid)
        setCheckingNetwork(false)
        if (!ssid) doScan()
      })
    return () => { cancelled = true }
  }, [])

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logLines])
  useEffect(() => {
    benchEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [benchLog])

  // ── scan ───────────────────────────────────────────────────────────────────

  async function doScan() {
    setScanning(true)
    setScanError('')
    setNetworks(null)
    setSelectedSSID(null)
    try {
      const res = await fetch('/api/wifi/scan')
      const data = await res.json()
      if (data.error) throw new Error(data.error)
      setNetworks(data)
    } catch (e: unknown) {
      setScanError(e instanceof Error ? e.message : 'Scan failed')
    } finally {
      setScanning(false)
    }
  }

  // ── benchmark (step 2) ────────────────────────────────────────────────────

  async function doBenchmark() {
    setStep(2)
    setBenchLog(['Starting 160 MB write test…'])
    setBenchResult(null)
    setBenchRunning(true)
    try {
      const res = await fetch('/api/benchmark/run', { method: 'POST',
        headers: { 'X-CSRF-Token': getCsrfToken() } })
      if (!res.ok || !res.body) throw new Error(res.statusText)
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const parts = buf.split('\n\n')
        buf = parts.pop() ?? ''
        for (const part of parts) {
          const line = part.replace(/^data: /, '').trim()
          if (!line) continue
          try {
            const parsed = JSON.parse(line)
            if (typeof parsed === 'string') setBenchLog(l => [...l, parsed])
            else if (parsed.result) setBenchResult(parsed.result)
            else if (parsed.done || parsed.error) { /* handled below */ }
          } catch { setBenchLog(l => [...l, line]) }
        }
      }
    } catch { /* benchmark optional — continue anyway */ }
    finally { setBenchRunning(false) }
  }

  // ── apply (step 3) ────────────────────────────────────────────────────────

  async function doApply() {
    setStep(3)
    setLogLines([])
    setApplyDone(false)
    setApplyError('')

    // If a WiFi network was selected, save it first.
    if (selectedSSID) {
      try {
        const res = await fetch('/api/wifi/networks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json',
                     'X-CSRF-Token': getCsrfToken() },
          body: JSON.stringify({ ssid: selectedSSID, password: wifiPw }),
        })
        if (!res.ok && res.status !== 409) {
          const err = await res.json().catch(() => ({}))
          appendLine(`⚠ WiFi: ${err.error ?? res.statusText}`)
        }
      } catch {
        appendLine('⚠ Could not save WiFi profile — continuing anyway.')
      }
    }

    const body = {
      archive_type: archiveType,
      archive_host: archiveHost,
      share_name:   shareName,
      username,
      password,
    }

    try {
      const res = await fetch('/api/setup/apply', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json',
                   'X-CSRF-Token': getCsrfToken() },
        body: JSON.stringify(body),
      })
      if (!res.ok || !res.body) {
        const text = await res.text().catch(() => res.statusText)
        setApplyError(text)
        return
      }

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const parts = buf.split('\n\n')
        buf = parts.pop() ?? ''
        for (const part of parts) {
          const line = part.replace(/^data: /, '')
          if (!line) continue
          try {
            const parsed = JSON.parse(line)
            if (typeof parsed === 'string') {
              appendLine(parsed)
            } else if (parsed.done) {
              setApplyDone(true)
            } else if (parsed.error) {
              setApplyError(parsed.error)
            }
          } catch {
            appendLine(line)
          }
        }
      }
    } catch (e: unknown) {
      setApplyError(e instanceof Error ? e.message : 'Apply failed')
    }
  }

  function appendLine(line: string) {
    setLogLines(prev => [...prev, line])
  }

  // ── render ─────────────────────────────────────────────────────────────────

  return (
    <div style={{
      minHeight: '100svh',
      background: 'var(--bg)',
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'center',
      padding: 'var(--sp4)',
    }}>
      <div style={{
        width: '100%',
        maxWidth: 520,
        marginTop: 'var(--sp6)',
      }}>
        {/* header */}
        <div style={{ marginBottom: 'var(--sp5)' }}>
          <h1 style={{
            fontSize: 'var(--ts-xl)',
            fontWeight: 700,
            letterSpacing: '-0.02em',
            color: 'var(--fg-0)',
          }}>
            Dash<span style={{ color: 'var(--accent)' }}>vault</span>
          </h1>
          <p style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)', marginTop: 'var(--sp1)' }}>
            First-time setup wizard
          </p>
        </div>

        {/* step indicator */}
        <StepIndicator current={step} total={4} />

        {/* card */}
        <div style={{
          background: 'var(--bg-1)',
          border: '1px solid var(--border)',
          borderRadius: 'var(--radius-md)',
          padding: 'var(--sp6)',
          marginTop: 'var(--sp4)',
        }}>
          {step === 0 && (
            <StepNetwork
              connectedSSID={changingNetwork ? null : connectedSSID}
              keepSSID={changingNetwork ? connectedSSID : null}
              checking={checkingNetwork}
              onChangeNetwork={() => { setChangingNetwork(true); doScan() }}
              onKeepNetwork={() => { setChangingNetwork(false); setSelectedSSID(null); setWifiPw('') }}
              networks={networks}
              scanning={scanning}
              scanError={scanError}
              selectedSSID={selectedSSID}
              wifiPw={wifiPw}
              showWifiPw={showWifiPw}
              onRescan={doScan}
              onSelect={setSelectedSSID}
              onWifiPwChange={setWifiPw}
              onToggleWifiPw={() => setShowWifiPw(v => !v)}
              onNext={() => setStep(1)}
              onSkip={() => { setSelectedSSID(null); setStep(1) }}
            />
          )}

          {step === 1 && (
            <StepArchive
              archiveType={archiveType}
              archiveHost={archiveHost}
              shareName={shareName}
              username={username}
              password={password}
              showPw={showSmBPw}
              onTypeChange={setArchiveType}
              onHostChange={setArchiveHost}
              onShareChange={setShareName}
              onUserChange={setUsername}
              onPassChange={setPassword}
              onTogglePw={() => setShowSmbPw(v => !v)}
              onBack={() => setStep(0)}
              onNext={doBenchmark}
            />
          )}

          {step === 2 && (
            <StepBenchmark
              log={benchLog}
              result={benchResult}
              running={benchRunning}
              logEndRef={benchEndRef}
              onBack={() => setStep(1)}
              onContinue={doApply}
            />
          )}

          {step === 3 && (
            <StepApplying
              lines={logLines}
              done={applyDone}
              error={applyError}
              logEndRef={logEndRef}
              onGoHome={() => navigate({ to: '/' })}
            />
          )}
        </div>
      </div>
    </div>
  )
}

// ── step 0: network selection ─────────────────────────────────────────────────

interface StepNetworkProps {
  connectedSSID: string | null
  keepSSID: string | null
  checking: boolean
  onChangeNetwork: () => void
  onKeepNetwork: () => void
  networks: ScannedNetwork[] | null
  scanning: boolean
  scanError: string
  selectedSSID: string | null
  wifiPw: string
  showWifiPw: boolean
  onRescan: () => void
  onSelect: (ssid: string) => void
  onWifiPwChange: (v: string) => void
  onToggleWifiPw: () => void
  onNext: () => void
  onSkip: () => void
}

function StepNetwork(p: StepNetworkProps) {
  if (p.checking) return <Scanning />

  if (p.connectedSSID) {
    return (
      <>
        <SectionTitle>Your network</SectionTitle>
        <Banner type="ok">Connected to “{p.connectedSSID}”</Banner>
        <PrimaryButton onClick={p.onNext}>Continue →</PrimaryButton>
        <SkipLink onClick={p.onChangeNetwork}>Use a different network</SkipLink>
      </>
    )
  }

  return (
    <>
      <SectionTitle>Connect to your network</SectionTitle>

      {p.scanning && <Scanning />}

      {!p.scanning && p.scanError && (
        <Banner type="err">{p.scanError}</Banner>
      )}

      {!p.scanning && p.networks && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--sp2)', marginBottom: 'var(--sp4)' }}>
          {p.networks.map(net => (
            <NetworkItem
              key={net.ssid}
              net={net}
              selected={p.selectedSSID === net.ssid}
              onClick={() => p.onSelect(net.ssid)}
            />
          ))}
        </div>
      )}

      {!p.scanning && (
        <div style={{ textAlign: 'right', marginBottom: 'var(--sp4)' }}>
          <SecondaryButton onClick={p.onRescan}>↺ Rescan</SecondaryButton>
        </div>
      )}

      {p.selectedSSID && (
        <Field label={`Password for "${p.selectedSSID}"`}>
          <PwInput
            id="wifi-pw"
            value={p.wifiPw}
            show={p.showWifiPw}
            placeholder="Leave blank if open network"
            onChange={e => p.onWifiPwChange(e.target.value)}
            onToggle={p.onToggleWifiPw}
          />
        </Field>
      )}

      <PrimaryButton onClick={p.onNext} disabled={!p.selectedSSID}>
        Next →
      </PrimaryButton>
      {p.keepSSID
        ? <SkipLink onClick={p.onKeepNetwork}>Keep “{p.keepSSID}”</SkipLink>
        : <SkipLink onClick={p.onSkip}>Skip — keep the current connection</SkipLink>}
    </>
  )
}

function NetworkItem({ net, selected, onClick }: { net: ScannedNetwork; selected: boolean; onClick: () => void }) {
  return (
    <div
      onClick={onClick}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--sp3)',
        padding: 'var(--sp3)',
        background: 'var(--bg-2)',
        border: `2px solid ${selected ? 'var(--accent)' : 'transparent'}`,
        borderRadius: 8,
        cursor: 'pointer',
        transition: 'border-color .15s',
      }}
    >
      <span style={{ fontFamily: 'monospace', fontSize: 18, flexShrink: 0 }}>
        {signalBars(net.signal)}
      </span>
      <span style={{ flex: 1, fontWeight: 500, wordBreak: 'break-all' }}>
        {net.ssid}
      </span>
      <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)', flexShrink: 0 }}>
        {net.security}
      </span>
    </div>
  )
}

// ── step 1: archive config ────────────────────────────────────────────────────

interface StepArchiveProps {
  archiveType: ArchiveType
  archiveHost: string
  shareName: string
  username: string
  password: string
  showPw: boolean
  onTypeChange: (v: ArchiveType) => void
  onHostChange: (v: string) => void
  onShareChange: (v: string) => void
  onUserChange: (v: string) => void
  onPassChange: (v: string) => void
  onTogglePw: () => void
  onBack: () => void
  onNext: () => void
}

function StepArchive(p: StepArchiveProps) {
  const needsHost = p.archiveType !== 'none'
  const isCifs    = p.archiveType === 'cifs'
  const isNfs     = p.archiveType === 'nfs'
  const isRsync   = p.archiveType === 'rsync'
  const canApply  = !needsHost || (p.archiveHost.trim() && p.shareName.trim() && (!isRsync || p.username.trim()))

  return (
    <>
      <SectionTitle>Archive destination</SectionTitle>

      <Field label="Archive type">
        <select
          value={p.archiveType}
          onChange={e => p.onTypeChange(e.target.value as ArchiveType)}
          style={inputStyle}
        >
          <option value="cifs">SMB / Windows share (CIFS)</option>
          <option value="nfs">NFS share</option>
          <option value="rclone">rclone (cloud storage)</option>
          <option value="rsync">rsync over SSH</option>
          <option value="none">None — local storage only</option>
        </select>
      </Field>

      {p.archiveType === 'none' && (
        <Banner type="warn">
          Clips will be stored on the Pi's SD card only — no remote backup.
        </Banner>
      )}

      {needsHost && (
        <>
          <Field label="Host (IP or hostname)">
            <input
              style={inputStyle}
              type="text"
              value={p.archiveHost}
              placeholder="192.168.1.100"
              autoComplete="off"
              onChange={e => p.onHostChange(e.target.value)}
            />
          </Field>
          <Field label="Share name">
            <input
              style={inputStyle}
              type="text"
              value={p.shareName}
              placeholder={isNfs ? '/volume1/teslacam' : 'TeslaCam'}
              autoComplete="off"
              onChange={e => p.onShareChange(e.target.value)}
            />
          </Field>
        </>
      )}

      {(isCifs || isRsync) && (
        <Field label={isRsync ? 'SSH Username' : 'Username (optional)'}>
          <input
            style={inputStyle}
            type="text"
            value={p.username}
            autoComplete="off"
            onChange={e => p.onUserChange(e.target.value)}
          />
        </Field>
      )}
      {isCifs && (
        <Field label="Password (optional)">
          <PwInput
            id="smb-pw"
            value={p.password}
            show={p.showPw}
            onChange={e => p.onPassChange(e.target.value)}
            onToggle={p.onTogglePw}
          />
        </Field>
      )}

      <div style={{ display: 'flex', gap: 'var(--sp3)', marginTop: 'var(--sp2)' }}>
        <SecondaryButton onClick={p.onBack}>← Back</SecondaryButton>
        <PrimaryButton onClick={p.onNext} disabled={!canApply} style={{ flex: 1 }}>
          Next →
        </PrimaryButton>
      </div>
    </>
  )
}

// ── step 2: benchmark ─────────────────────────────────────────────────────────

interface BenchmarkRunResult {
  timestamp: string
  write_mbps: number | null
  rating: 'good' | 'marginal' | 'slow' | 'unknown'
  timed_out: boolean
  size_mb: number
  elapsed_ms: number
}

function StepBenchmark({ log, result, running, logEndRef, onBack, onContinue }: {
  log: string[]
  result: BenchmarkRunResult | null
  running: boolean
  logEndRef: React.RefObject<HTMLDivElement | null>
  onBack: () => void
  onContinue: () => void
}) {
  const ratingColor = (r: string) =>
    r === 'good' ? 'var(--ok)' : r === 'marginal' ? 'var(--warn)' : 'var(--err)'

  const resultLabel = result
    ? result.write_mbps != null
      ? `${result.write_mbps.toFixed(1)} MB/s — ${result.rating}`
      : 'Timed out — card is too slow'
    : null

  return (
    <>
      <SectionTitle>Checking SD card speed</SectionTitle>
      <p style={{ color: 'var(--fg-1)', fontSize: 'var(--ts-sm)', marginBottom: 'var(--sp4)' }}>
        Tesla records at ~20 MB/s. A slow card can cause missed or corrupted clips.
        This writes 160 MB to measure your card's real-world speed.
      </p>

      {/* log box */}
      <div style={{
        background: 'var(--bg)',
        border: '1px solid var(--border)',
        borderRadius: 6,
        padding: 'var(--sp3)',
        fontFamily: 'var(--font-mono)',
        fontSize: 'var(--ts-sm)',
        color: 'var(--fg-1)',
        maxHeight: 140,
        overflowY: 'auto',
        marginBottom: 'var(--sp4)',
      }}>
        {log.length === 0 && running && (
          <span style={{ color: 'var(--fg-2)' }}>Running…</span>
        )}
        {log.map((l, i) => <div key={i}>{l}</div>)}
        {result && (
          <div style={{ color: ratingColor(result.rating), fontWeight: 600, marginTop: 4 }}>
            → {resultLabel}
          </div>
        )}
        <div ref={logEndRef} />
      </div>

      {/* result banner */}
      {result && !running && (
        <div style={{
          padding: '10px 14px',
          borderRadius: 6,
          marginBottom: 'var(--sp4)',
          fontSize: 'var(--ts-sm)',
          ...(result.rating === 'good'
            ? { background: '#052e16', border: '1px solid #166534', color: '#86efac' }
            : result.rating === 'marginal'
            ? { background: '#451a03', border: '1px solid #92400e', color: '#fcd34d' }
            : { background: '#450a0a', border: '1px solid #991b1b', color: '#fca5a5' }),
        }}>
          {result.rating === 'good' && '✓ Your SD card looks good for TeslaCam use.'}
          {result.rating === 'marginal' && '⚠ Your SD card is marginal — clips may be lost under heavy load. Consider a faster card.'}
          {result.rating === 'slow' && '✗ Your SD card is too slow for reliable TeslaCam recording. We recommend replacing it.'}
        </div>
      )}

      <div style={{ display: 'flex', gap: 'var(--sp3)' }}>
        <SecondaryButton onClick={onBack}>← Back</SecondaryButton>
        <PrimaryButton
          onClick={onContinue}
          disabled={running}
          style={{ flex: 1 }}
        >
          {running ? 'Testing…' : 'Continue →'}
        </PrimaryButton>
      </div>
      {!running && (
        <SkipLink onClick={onContinue}>Skip benchmark and continue</SkipLink>
      )}
    </>
  )
}

// ── step 3: applying ──────────────────────────────────────────────────────────

function StepApplying({
  lines, done, error, logEndRef, onGoHome,
}: {
  lines: string[]
  done: boolean
  error: string
  logEndRef: React.RefObject<HTMLDivElement | null>
  onGoHome: () => void
}) {
  return (
    <>
      <SectionTitle>{done ? 'Setup complete 🎉' : error ? 'Setup failed' : 'Applying configuration…'}</SectionTitle>

      {/* log box */}
      <div style={{
        background: 'var(--bg)',
        border: '1px solid var(--border)',
        borderRadius: 6,
        padding: 'var(--sp3)',
        fontFamily: 'var(--font-mono)',
        fontSize: 'var(--ts-sm)',
        color: 'var(--fg-1)',
        maxHeight: 260,
        overflowY: 'auto',
        marginBottom: 'var(--sp4)',
        minHeight: 80,
      }}>
        {lines.length === 0 && !done && !error && (
          <span style={{ color: 'var(--fg-2)', animation: 'pulse 1.4s ease-in-out infinite' }}>
            Running…
          </span>
        )}
        {lines.map((l, i) => (
          <div key={i}>{l}</div>
        ))}
        <div ref={logEndRef} />
      </div>

      {error && <Banner type="err">{error}</Banner>}

      {done && (
        <>
          <Banner type="ok">
            Configuration complete! Connect your Pi to your network and open&nbsp;
            <a href="http://dashvault.local" style={{ color: 'var(--ok)' }}>
              http://dashvault.local
            </a>
          </Banner>
          <PrimaryButton onClick={onGoHome} style={{ marginTop: 'var(--sp4)' }}>
            Go to Dashboard →
          </PrimaryButton>
        </>
      )}
    </>
  )
}

// ── shared small components ────────────────────────────────────────────────────

function StepIndicator({ current, total }: { current: number; total: number }) {
  return (
    <div style={{ display: 'flex', gap: 'var(--sp2)', marginBottom: 'var(--sp4)' }}>
      {Array.from({ length: total }, (_, i) => (
        <div key={i} style={{
          width: 8, height: 8, borderRadius: '50%',
          background: i < current ? 'var(--ok)' : i === current ? 'var(--accent)' : 'var(--bg-4)',
          border: `2px solid ${i < current ? 'var(--ok)' : i === current ? 'var(--accent)' : 'var(--border)'}`,
          transition: 'background .2s, border-color .2s',
        }} />
      ))}
    </div>
  )
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <h2 style={{
      fontSize: 'var(--ts-lg)',
      fontWeight: 600,
      color: 'var(--fg-0)',
      marginBottom: 'var(--sp4)',
    }}>
      {children}
    </h2>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: 'var(--sp4)' }}>
      <label style={{ display: 'block', fontSize: 'var(--ts-sm)', color: 'var(--fg-1)', marginBottom: 'var(--sp1)' }}>
        {label}
      </label>
      {children}
    </div>
  )
}

const inputStyle: React.CSSProperties = {
  width: '100%',
  background: 'var(--bg-2)',
  border: '1px solid var(--border)',
  borderRadius: 6,
  padding: '8px 12px',
  color: 'var(--fg-0)',
  fontSize: 'var(--ts-md)',
  outline: 'none',
}

function PwInput({ id, value, show, placeholder, onChange, onToggle }: {
  id: string
  value: string
  show: boolean
  placeholder?: string
  onChange: React.ChangeEventHandler<HTMLInputElement>
  onToggle: () => void
}) {
  return (
    <div style={{ position: 'relative' }}>
      <input
        id={id}
        type={show ? 'text' : 'password'}
        value={value}
        placeholder={placeholder}
        autoComplete="off"
        onChange={onChange}
        style={{ ...inputStyle, paddingRight: 44 }}
      />
      <button
        type="button"
        onClick={onToggle}
        style={{
          position: 'absolute', right: 10, top: '50%', transform: 'translateY(-50%)',
          background: 'none', border: 'none', cursor: 'pointer',
          color: 'var(--fg-2)', fontSize: 18, lineHeight: 1, padding: 4,
        }}
      >
        {show ? '🙈' : '👁'}
      </button>
    </div>
  )
}

function PrimaryButton({ children, onClick, disabled, style }: {
  children: React.ReactNode
  onClick: () => void
  disabled?: boolean
  style?: React.CSSProperties
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      style={{
        width: '100%',
        padding: '10px 16px',
        background: 'var(--accent)',
        border: 'none',
        borderRadius: 6,
        color: '#fff',
        fontSize: 'var(--ts-md)',
        fontWeight: 600,
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.45 : 1,
        transition: 'opacity .15s',
        ...style,
      }}
    >
      {children}
    </button>
  )
}

function SecondaryButton({ children, onClick }: { children: React.ReactNode; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      style={{
        background: 'none',
        border: '1px solid var(--border)',
        borderRadius: 6,
        color: 'var(--fg-1)',
        fontSize: 'var(--ts-sm)',
        padding: '8px 12px',
        cursor: 'pointer',
      }}
    >
      {children}
    </button>
  )
}

function SkipLink({ children, onClick }: { children: React.ReactNode; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      style={{
        background: 'none',
        border: 'none',
        color: 'var(--fg-2)',
        fontSize: 'var(--ts-sm)',
        textDecoration: 'underline',
        cursor: 'pointer',
        marginTop: 'var(--sp3)',
        display: 'block',
        width: '100%',
        textAlign: 'center',
      }}
    >
      {children}
    </button>
  )
}

function Scanning() {
  return (
    <div style={{
      textAlign: 'center',
      color: 'var(--fg-2)',
      fontSize: 'var(--ts-sm)',
      padding: 'var(--sp5)',
      animation: 'pulse 1.4s ease-in-out infinite',
    }}>
      Scanning for networks…
    </div>
  )
}

function Banner({ type, children }: { type: 'warn' | 'ok' | 'err'; children: React.ReactNode }) {
  const styles: Record<string, React.CSSProperties> = {
    warn: { background: '#451a03', border: '1px solid #92400e', color: '#fcd34d' },
    ok:   { background: '#052e16', border: '1px solid #166534', color: '#86efac' },
    err:  { background: '#450a0a', border: '1px solid #991b1b', color: '#fca5a5' },
  }
  return (
    <div style={{
      padding: '10px 14px',
      borderRadius: 6,
      fontSize: 'var(--ts-sm)',
      marginBottom: 'var(--sp4)',
      ...styles[type],
    }}>
      {children}
    </div>
  )
}

// ── utils ──────────────────────────────────────────────────────────────────────

function signalBars(signal: number): string {
  if (signal >= 75) return '▂▄▆█'
  if (signal >= 50) return '▂▄▆░'
  if (signal >= 25) return '▂▄░░'
  return '▂░░░'
}

function getCsrfToken(): string {
  return document.cookie
    .split('; ')
    .find(r => r.startsWith('teslausb_csrf='))
    ?.split('=')[1] ?? ''
}
