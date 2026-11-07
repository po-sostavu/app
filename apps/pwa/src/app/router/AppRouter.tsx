import { BrowserRouter, Route, Routes } from 'react-router';

import { HomePage } from '@/pages/home';
import { NotFoundPage } from '@/pages/not-found';
import { ProfilePage } from '@/pages/profile';

import { AppLayout } from '../layouts/AppLayout';

export function AppRouter() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<AppLayout />}>
          <Route index element={<HomePage />} />
          <Route path="profile" element={<ProfilePage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
