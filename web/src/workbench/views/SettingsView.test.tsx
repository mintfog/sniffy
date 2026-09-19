import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test } from 'vitest'
import {
  bridgeName,
  emitUpdate,
  resetWails,
  updateState,
  wails,
} from '@/test/wails'
import { SettingsView } from './SettingsView'

test('自动检查开关以后端状态为准，并跟随其它窗口的改动', async () => {
  resetWails()
  wails.handlers.set('GetConfig', () => ({}))
  const user = userEvent.setup()
  render(<SettingsView />)
  const field = screen.getByText('自动检查更新').parentElement!.parentElement!
  const toggle = within(field).getByRole('switch')
  await waitFor(() => expect(toggle).toBeChecked())
  wails.handlers.set('SetUpdateAutoCheck', () =>
    updateState({ revision: 2, autoCheck: false })
  )
  await user.click(toggle)
  expect(wails.call).toHaveBeenCalledWith(
    bridgeName('SetUpdateAutoCheck'),
    false
  )
  await waitFor(() => expect(toggle).not.toBeChecked())
  act(() => emitUpdate(updateState({ revision: 3, autoCheck: true })))
  expect(toggle).toBeChecked()
})
