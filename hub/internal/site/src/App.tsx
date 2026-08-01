import { useStore } from '@nanostores/react'
import { $authed } from '@/stores/auth'
import { $route } from '@/router'
import { Login } from '@/pages/Login'
import { Shell } from '@/components/Shell'
import { Placeholder } from '@/components/Placeholder'
import { Machines } from '@/pages/Machines'
import { MachineDetail } from '@/pages/MachineDetail'
import { Settings } from '@/pages/Settings'
import { ConfigSets } from '@/pages/ConfigSets'
import { ConfigSetDetail } from '@/pages/ConfigSetDetail'
import { Credentials } from '@/pages/Credentials'
import { ImportWizard } from '@/pages/ImportWizard'

export function App() {
  const authed = useStore($authed)
  const route = useStore($route)
  if (!authed) return <Login />

  return (
    <Shell>
      {route.key === 'machines' && (route.param ? <MachineDetail id={route.param} /> : <Machines />)}
      {route.key === 'settings' && <Settings />}
      {route.key === 'configsets' && (route.param ? <ConfigSetDetail id={route.param} /> : <ConfigSets />)}
      {route.key === 'credentials' && <Credentials />}
      {route.key === 'import' && <ImportWizard machineId={route.param} />}
      {route.key === 'inbox' && <Placeholder milestone="M1" />}
      {(route.key === 'overview' || route.key === 'usage' || route.key === 'subscriptions') && (
        <Placeholder milestone="M2" />
      )}
    </Shell>
  )
}
