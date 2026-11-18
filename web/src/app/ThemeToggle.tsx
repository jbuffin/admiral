import { Icon } from './Icon';
import { theme, toggleTheme } from './theme';

export function ThemeToggle() {
  const label = theme.value === 'dark' ? 'Switch to light mode' : 'Switch to dark mode';
  return (
    <button type="button" class="theme-toggle" aria-label={label} title={label} onClick={toggleTheme}>
      <Icon name={theme.value === 'dark' ? 'sun' : 'moon'} size={18} />
    </button>
  );
}
