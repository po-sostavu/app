export function getInstallInstructions(userAgent: string, maxTouchPoints: number) {
  const ios =
    /iPhone|iPad|iPod/i.test(userAgent) || (/Macintosh/i.test(userAgent) && maxTouchPoints > 1);

  if (ios) {
    return {
      steps: [
        'В меню браузера откройте «Поделиться» и выберите «На экран Домой»',
        'Если есть переключатель «Открывать как веб-приложение», включите его',
        'Нажмите «Добавить» и откройте ПоСоставу с главного экрана',
      ],
      note: 'Если нужного пункта нет, откройте эту страницу в Safari',
    };
  }

  if (/Android/i.test(userAgent)) {
    return {
      steps: [
        'Откройте меню браузера рядом с адресной строкой',
        'Выберите «Установить приложение» или «Добавить на главный экран»',
        'Подтвердите установку и откройте ПоСоставу по появившемуся значку',
      ],
      note: 'Название пункта зависит от браузера. Если его нет, откройте страницу в Chrome или Edge',
    };
  }

  if (
    /Macintosh/i.test(userAgent) &&
    /Safari/i.test(userAgent) &&
    !/Chrome|Chromium|Edg|OPR|Firefox/i.test(userAgent)
  ) {
    return {
      steps: [
        'В меню Safari выберите «Файл» → «Добавить в Dock»',
        'Нажмите «Добавить» и откройте ПоСоставу из Dock',
      ],
      note: 'Нужна macOS Sonoma 14 или новее. Если команды нет, попробуйте Chrome или Edge',
    };
  }

  return {
    steps: [
      'Нажмите значок установки рядом с адресом страницы или откройте меню браузера',
      'Выберите установку сайта как приложения и подтвердите действие',
    ],
    note: 'Если команды установки нет, откройте эту страницу в Chrome или Edge',
  };
}
