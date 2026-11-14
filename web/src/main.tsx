import { App as AntApp, ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import { WorkspaceProvider } from './data/store'
import { applyTheme, currentTheme, ThemeProvider } from './theme'
import './styles.css'

applyTheme(currentTheme())

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider locale={zhCN}>
      {/* 两个字的按钮不要自动加空格（「打 开」） */}
      <ConfigProvider button={{ autoInsertSpace: false }}>
        <AntApp>
          <WorkspaceProvider>
            <App />
          </WorkspaceProvider>
        </AntApp>
      </ConfigProvider>
    </ThemeProvider>
  </StrictMode>
)
