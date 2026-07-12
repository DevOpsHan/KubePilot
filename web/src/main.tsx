import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { I18nextProvider } from 'react-i18next'
import { appI18n } from './i18n'
import './index.css'
import App from './App.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nextProvider i18n={appI18n}>
      <App />
    </I18nextProvider>
  </StrictMode>,
)
