// PaySplit Admin Portal — Interactive Logic & API Client
(function () {
  'use strict';

  // State Management
  const state = {
    apiBaseUrl: localStorage.getItem('paysplit_admin_api_url') || (window.location.origin.includes('http') ? window.location.origin : 'http://localhost:8080'),
    token: localStorage.getItem('paysplit_admin_token') || '',
    refreshToken: localStorage.getItem('paysplit_admin_refresh_token') || '',
    currentUser: null,
    currentTab: 'tab-overview',
    autoRefreshTimer: null,
    
    // Accounts Query State
    accounts: {
      page: 1,
      limit: 20,
      search: '',
      status: '',
      role: '',
      sortBy: 'created_at',
      sortOrder: 'desc',
      totalPages: 1,
      totalItems: 0,
      items: [],
      isLoading: false
    },
    
    // Selected User for Detail/Status
    selectedUserId: null,
    selectedUserDetail: null
  };

  // Helper: DOM Elements Cache
  const el = {
    authOverlay: document.getElementById('auth-overlay'),
    appLayout: document.getElementById('app-layout'),
    headerUserSection: document.getElementById('header-user-section'),
    adminName: document.getElementById('admin-name'),
    adminAvatarIcon: document.getElementById('admin-avatar-icon'),
    statusText: document.getElementById('status-text'),
    systemStatusIndicator: document.getElementById('system-status-indicator'),
    
    // Login Form
    loginForm: document.getElementById('login-form'),
    loginEmail: document.getElementById('login-email'),
    loginPassword: document.getElementById('login-password'),
    loginApiUrl: document.getElementById('login-api-url'),
    btnSubmitLogin: document.getElementById('btn-submit-login'),
    btnLogout: document.getElementById('btn-logout'),
    
    // Navigation
    navItems: document.querySelectorAll('.nav-item'),
    tabPanes: document.querySelectorAll('.tab-pane'),
    sidebarUptime: document.getElementById('sidebar-uptime'),
    
    // Overview Controls
    btnRefreshOverview: document.getElementById('btn-refresh-overview'),
    toggleAutoRefresh: document.getElementById('toggle-auto-refresh'),
    
    // Accounts Controls
    btnRefreshAccounts: document.getElementById('btn-refresh-accounts'),
    filterSearch: document.getElementById('filter-search'),
    btnClearSearch: document.getElementById('btn-clear-search'),
    filterStatus: document.getElementById('filter-status'),
    filterRole: document.getElementById('filter-role'),
    filterSortBy: document.getElementById('filter-sort-by'),
    filterSortOrder: document.getElementById('filter-sort-order'),
    accountsTableBody: document.getElementById('accounts-table-body'),
    pagStart: document.getElementById('pag-start'),
    pagEnd: document.getElementById('pag-end'),
    pagTotal: document.getElementById('pag-total'),
    pagLimit: document.getElementById('pag-limit'),
    pagCurrent: document.getElementById('pag-current'),
    btnPagPrev: document.getElementById('btn-pag-prev'),
    btnPagNext: document.getElementById('btn-pag-next'),
    
    // Modals
    modalAccountDetail: document.getElementById('modal-account-detail'),
    detailModalBody: document.getElementById('detail-modal-body'),
    detailDisplayName: document.getElementById('detail-display-name'),
    detailEmail: document.getElementById('detail-email'),
    btnCloseDetail: document.getElementById('btn-close-detail'),
    btnCloseDetailFooter: document.getElementById('btn-close-detail-footer'),
    btnOpenStatusFromDetail: document.getElementById('btn-open-status-from-detail'),
    
    modalUpdateStatus: document.getElementById('modal-update-status'),
    formUpdateStatus: document.getElementById('form-update-status'),
    statusTargetUserId: document.getElementById('status-target-user-id'),
    statusTargetUserName: document.getElementById('status-target-user-name'),
    selectNewStatus: document.getElementById('select-new-status'),
    inputStatusReason: document.getElementById('input-status-reason'),
    reasonRequiredStar: document.getElementById('reason-required-star'),
    btnCloseStatus: document.getElementById('btn-close-status'),
    btnCloseStatusFooter: document.getElementById('btn-close-status-footer'),
    btnSubmitStatus: document.getElementById('btn-submit-status'),
    
    // API Config Modal
    modalApiConfig: document.getElementById('modal-api-config'),
    cfgApiUrl: document.getElementById('cfg-api-url'),
    btnApiConfig: document.getElementById('btn-api-config'),
    btnCloseApiConfig: document.getElementById('btn-close-api-config'),
    btnCloseApiConfigFooter: document.getElementById('btn-close-api-config-footer'),
    btnSaveApiConfig: document.getElementById('btn-save-api-config'),
    
    // Toast Container
    toastContainer: document.getElementById('toast-container')
  };

  // ==========================================
  // API CLIENT HELPER
  // ==========================================
  async function apiRequest(endpoint, options = {}) {
    const url = `${state.apiBaseUrl.replace(/\/$/, '')}${endpoint}`;
    const headers = {
      'Content-Type': 'application/json',
      ...options.headers
    };

    if (state.token) {
      headers['Authorization'] = `Bearer ${state.token}`;
    }

    try {
      const response = await fetch(url, {
        ...options,
        headers
      });

      // Special handling for 401 Unauthorized
      if (response.status === 401) {
        showToast('Phiên làm việc đã hết hạn hoặc không hợp lệ. Vui lòng đăng nhập lại.', 'error');
        handleLogout();
        throw new Error('Unauthorized (401)');
      }

      // Handle responses without content
      if (response.status === 204) {
        return { success: true };
      }

      const data = await response.json().catch(() => ({}));

      if (!response.ok) {
        const errorMsg = data.error?.message || data.message || `Lỗi yêu cầu (${response.status})`;
        const errorObj = new Error(errorMsg);
        errorObj.status = response.status;
        errorObj.code = data.error?.code;
        errorObj.details = data.error?.details;
        throw errorObj;
      }

      return data;
    } catch (err) {
      console.error(`API Request failed for ${endpoint}:`, err);
      throw err;
    }
  }

  // ==========================================
  // TOAST NOTIFICATION SYSTEM
  // ==========================================
  function showToast(message, type = 'info', duration = 4000) {
    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;
    
    let icon = 'ℹ️';
    if (type === 'success') icon = '✅';
    if (type === 'error') icon = '❌';
    if (type === 'warning') icon = '⚠️';

    toast.innerHTML = `<span>${icon}</span> <span>${escapeHtml(message)}</span>`;
    el.toastContainer.appendChild(toast);

    setTimeout(() => {
      toast.style.opacity = '0';
      toast.style.transform = 'translateX(20px)';
      toast.style.transition = 'all 0.3s ease';
      setTimeout(() => toast.remove(), 300);
    }, duration);
  }

  function escapeHtml(str) {
    if (!str) return '';
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }

  function formatMoneyVND(amount) {
    if (amount === undefined || amount === null) return '0 đ';
    return Number(amount).toLocaleString('vi-VN') + ' đ';
  }

  function formatDate(dateStr) {
    if (!dateStr) return '—';
    try {
      const d = new Date(dateStr);
      if (isNaN(d.getTime())) return dateStr;
      return d.toLocaleString('vi-VN', {
        day: '2-digit',
        month: '2-digit',
        year: 'numeric',
        hour: '2-digit',
        minute: '2-digit'
      });
    } catch {
      return dateStr;
    }
  }

  function formatUptime(seconds) {
    if (!seconds || seconds < 0) return '0s';
    const d = Math.floor(seconds / 86400);
    const h = Math.floor((seconds % 86400) / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = Math.floor(seconds % 60);
    if (d > 0) return `${d}d ${h}h ${m}m ${s}s`;
    if (h > 0) return `${h}h ${m}m ${s}s`;
    if (m > 0) return `${m}m ${s}s`;
    return `${s}s`;
  }

  // ==========================================
  // INITIALIZATION & AUTH CHECK
  // ==========================================
  function init() {
    el.loginApiUrl.value = state.apiBaseUrl;
    el.cfgApiUrl.value = state.apiBaseUrl;

    bindEvents();
    checkHealthStatus();

    // Check saved user session
    const savedUserJson = localStorage.getItem('paysplit_admin_user');
    if (state.token && savedUserJson) {
      try {
        state.currentUser = JSON.parse(savedUserJson);
        showDashboard();
      } catch {
        showLogin();
      }
    } else {
      showLogin();
    }
  }

  function showLogin() {
    el.authOverlay.style.display = 'flex';
    el.appLayout.style.display = 'none';
    el.headerUserSection.style.display = 'none';
    if (state.autoRefreshTimer) {
      clearInterval(state.autoRefreshTimer);
      state.autoRefreshTimer = null;
    }
  }

  function showDashboard() {
    el.authOverlay.style.display = 'none';
    el.appLayout.style.display = 'flex';
    el.headerUserSection.style.display = 'flex';

    if (state.currentUser) {
      el.adminName.textContent = state.currentUser.display_name || state.currentUser.email || 'Admin';
      const initial = (state.currentUser.display_name || state.currentUser.email || 'A')[0].toUpperCase();
      el.adminAvatarIcon.textContent = initial;
    }

    // Load Initial Data
    switchTab('tab-overview');
    loadSystemOverview();
    startAutoRefresh();
  }

  async function checkHealthStatus() {
    try {
      const res = await fetch(`${state.apiBaseUrl.replace(/\/$/, '')}/health/ready`);
      const statusDot = el.systemStatusIndicator.querySelector('.status-dot');
      if (res.ok) {
        statusDot.className = 'status-dot online';
        el.statusText.textContent = 'Server: Online (Ready)';
      } else {
        statusDot.className = 'status-dot offline';
        el.statusText.textContent = 'Server: Degraded';
      }
    } catch {
      const statusDot = el.systemStatusIndicator.querySelector('.status-dot');
      statusDot.className = 'status-dot offline';
      el.statusText.textContent = 'Server: Offline';
    }
  }

  function getOrCreateDeviceId() {
    let deviceId = localStorage.getItem('paysplit_admin_device_id');
    if (!deviceId) {
      if (window.crypto && window.crypto.randomUUID) {
        deviceId = window.crypto.randomUUID();
      } else {
        deviceId = '10000000-1000-4000-8000-100000000000'.replace(/[018]/g, c =>
          (c ^ crypto.getRandomValues(new Uint8Array(1))[0] & 15 >> c / 4).toString(16)
        );
      }
      localStorage.setItem('paysplit_admin_device_id', deviceId);
    }
    return deviceId;
  }

  // ==========================================
  // AUTHENTICATION FLOW
  // ==========================================
  async function handleLogin(e) {
    e.preventDefault();
    const email = el.loginEmail.value.trim();
    const password = el.loginPassword.value;
    const customUrl = el.loginApiUrl.value.trim();

    if (customUrl) {
      state.apiBaseUrl = customUrl;
      localStorage.setItem('paysplit_admin_api_url', customUrl);
      el.cfgApiUrl.value = customUrl;
    }

    if (!email || !password) {
      showToast('Vui lòng nhập đầy đủ Email và Mật khẩu', 'warning');
      return;
    }

    const btnText = el.btnSubmitLogin.querySelector('.btn-text');
    const spinner = el.btnSubmitLogin.querySelector('.spinner');
    btnText.style.display = 'none';
    spinner.style.display = 'inline-block';
    el.btnSubmitLogin.disabled = true;

    try {
      const deviceId = getOrCreateDeviceId();
      const deviceName = 'Admin Web Portal';

      const result = await apiRequest('/api/v1/auth/sign-in', {
        method: 'POST',
        body: JSON.stringify({
          email,
          password,
          device_id: deviceId,
          device_name: deviceName
        })
      });

      const data = result.data || {};
      const user = data.user || {};

      if (user.role !== 'admin') {
        throw new Error('Tài khoản này không có vai trò Quản trị viên (Role: admin).');
      }

      state.token = data.access_token;
      state.refreshToken = data.refresh_token;
      state.currentUser = user;

      localStorage.setItem('paysplit_admin_token', state.token);
      localStorage.setItem('paysplit_admin_refresh_token', state.refreshToken);
      localStorage.setItem('paysplit_admin_user', JSON.stringify(user));

      showToast(`Chào mừng Admin ${user.display_name || user.email}!`, 'success');
      showDashboard();
    } catch (err) {
      showToast(err.message || 'Đăng nhập thất bại', 'error');
    } finally {
      btnText.style.display = 'inline-block';
      spinner.style.display = 'none';
      el.btnSubmitLogin.disabled = false;
    }
  }

  async function handleLogout() {
    try {
      if (state.token) {
        await apiRequest('/api/v1/auth/sign-out', { method: 'POST' }).catch(() => {});
      }
    } catch (e) {
      console.warn('Logout request warning:', e);
    } finally {
      state.token = '';
      state.refreshToken = '';
      state.currentUser = null;
      localStorage.removeItem('paysplit_admin_token');
      localStorage.removeItem('paysplit_admin_refresh_token');
      localStorage.removeItem('paysplit_admin_user');
      showLogin();
      showToast('Đã đăng xuất khỏi cổng Quản trị', 'info');
    }
  }

  // ==========================================
  // TAB NAVIGATION
  // ==========================================
  function switchTab(tabId) {
    state.currentTab = tabId;

    el.navItems.forEach(item => {
      if (item.dataset.tab === tabId) {
        item.classList.add('active');
      } else {
        item.classList.remove('active');
      }
    });

    el.tabPanes.forEach(pane => {
      if (pane.id === tabId) {
        pane.classList.add('active');
      } else {
        pane.classList.remove('active');
      }
    });

    if (tabId === 'tab-overview') {
      loadSystemOverview();
    } else if (tabId === 'tab-accounts') {
      loadAccounts();
    }
  }

  // ==========================================
  // SYSTEM OVERVIEW (AC-7)
  // ==========================================
  async function loadSystemOverview() {
    try {
      const res = await apiRequest('/api/v1/admin/system/overview');
      const ov = res.data || {};

      // Users Overview
      const users = ov.users || {};
      document.getElementById('stat-total-users').textContent = users.total || 0;
      document.getElementById('stat-users-active').textContent = users.active || 0;
      document.getElementById('stat-users-pending').textContent = users.pending_verification || 0;
      document.getElementById('stat-users-suspended').textContent = users.suspended || 0;
      document.getElementById('stat-users-locked').textContent = users.locked || 0;

      // Groups & Bills
      const groups = ov.groups || {};
      const bills = ov.bills || {};
      document.getElementById('stat-total-groups').textContent = groups.total || 0;
      document.getElementById('stat-bills-finalized').textContent = bills.total_finalized || 0;
      document.getElementById('stat-bills-draft').textContent = bills.total_draft || 0;

      // Debts Overview
      const debts = ov.debts || {};
      document.getElementById('stat-debts-awaiting').textContent = debts.awaiting || 0;
      document.getElementById('stat-debts-pending').textContent = debts.pending_confirmation || 0;
      document.getElementById('stat-debts-stalled').textContent = debts.stalled_confirmation || 0;
      document.getElementById('stat-debts-rejected').textContent = debts.rejected || 0;
      document.getElementById('stat-debts-settled').textContent = debts.settled || 0;

      // Jobs & OCR Overview
      const ocr = ov.ocr_jobs || {};
      const cleanup = ov.media_cleanup || {};
      const ocrTotal = (ocr.queued || 0) + (ocr.processing || 0) + (ocr.succeeded || 0) + (ocr.failed || 0);
      document.getElementById('stat-ocr-total').textContent = ocrTotal;
      document.getElementById('stat-ocr-queued').textContent = ocr.queued || 0;
      document.getElementById('stat-ocr-processing').textContent = ocr.processing || 0;
      document.getElementById('stat-ocr-succeeded').textContent = ocr.succeeded || 0;
      document.getElementById('stat-ocr-failed').textContent = ocr.failed || 0;
      document.getElementById('stat-media-cleanup').textContent = cleanup.pending_jobs_count || 0;

      // Runtime
      const runtime = ov.runtime || {};
      document.getElementById('stat-runtime-goroutines').textContent = runtime.goroutines_count || 0;
      const memMB = ((runtime.alloc_memory_bytes || 0) / (1024 * 1024)).toFixed(2);
      document.getElementById('stat-runtime-memory').textContent = `${memMB} MB`;
      const uptimeStr = formatUptime(runtime.uptime_seconds);
      document.getElementById('stat-runtime-uptime').textContent = uptimeStr;
      el.sidebarUptime.textContent = `Uptime: ${uptimeStr}`;

    } catch (err) {
      if (err.status !== 401) {
        showToast('Không thể tải tổng quan hệ thống: ' + err.message, 'error');
      }
    }
  }

  function startAutoRefresh() {
    if (state.autoRefreshTimer) clearInterval(state.autoRefreshTimer);
    if (el.toggleAutoRefresh.checked) {
      state.autoRefreshTimer = setInterval(() => {
        if (state.token && state.currentTab === 'tab-overview') {
          loadSystemOverview();
          checkHealthStatus();
        }
      }, 15000);
    }
  }

  // ==========================================
  // USER ACCOUNTS MANAGEMENT (AC-1, AC-2, AC-3)
  // ==========================================
  async function loadAccounts() {
    if (state.accounts.isLoading) return;
    state.accounts.isLoading = true;

    el.accountsTableBody.innerHTML = `
      <tr>
        <td colspan="7" class="text-center loading-cell">
          <div class="inline-spinner"></div>
          <span>Đang tải danh sách người dùng...</span>
        </td>
      </tr>
    `;

    try {
      const params = new URLSearchParams({
        page: state.accounts.page,
        limit: state.accounts.limit,
        sort_by: state.accounts.sortBy,
        sort_order: state.accounts.sortOrder
      });

      if (state.accounts.search) params.append('search', state.accounts.search);
      if (state.accounts.status) params.append('status', state.accounts.status);
      if (state.accounts.role) params.append('role', state.accounts.role);

      const res = await apiRequest(`/api/v1/admin/accounts?${params.toString()}`);
      const data = res.data || {};
      state.accounts.items = data.items || [];
      const pag = data.pagination || {};
      state.accounts.totalItems = pag.total || 0;
      state.accounts.totalPages = pag.total_pages || 1;

      renderAccountsTable();
      renderPagination();
    } catch (err) {
      if (err.status !== 401) {
        showToast('Không thể tải danh sách tài khoản: ' + err.message, 'error');
        el.accountsTableBody.innerHTML = `
          <tr>
            <td colspan="7" class="text-center" style="color:var(--color-danger); padding:2rem;">
              ⚠️ Lỗi khi tải dữ liệu: ${escapeHtml(err.message)}
            </td>
          </tr>
        `;
      }
    } finally {
      state.accounts.isLoading = false;
    }
  }

  function renderAccountsTable() {
    const items = state.accounts.items;
    if (!items || items.length === 0) {
      el.accountsTableBody.innerHTML = `
        <tr>
          <td colspan="7" class="text-center" style="padding:3rem 1rem; color:var(--text-muted);">
            Không tìm thấy người dùng nào phù hợp với bộ lọc.
          </td>
        </tr>
      `;
      return;
    }

    let html = '';
    items.forEach(u => {
      const initial = (u.display_name || u.email || 'U')[0].toUpperCase();
      const avatarHtml = u.avatar_url 
        ? `<div class="user-avatar-small"><img src="${escapeHtml(u.avatar_url)}" alt=""></div>`
        : `<div class="user-avatar-small">${initial}</div>`;

      // Status Badge
      let statusBadge = '';
      if (u.status === 'active') {
        statusBadge = `<span class="badge badge-active"><span class="badge-dot dot-active"></span>Hoạt động</span>`;
      } else if (u.status === 'pending_verification') {
        statusBadge = `<span class="badge badge-pending"><span class="badge-dot dot-pending"></span>Chờ xác thực</span>`;
      } else if (u.status === 'suspended') {
        statusBadge = `<span class="badge badge-suspended"><span class="badge-dot dot-suspended"></span>Đình chỉ</span>`;
      } else if (u.status === 'locked') {
        statusBadge = `<span class="badge badge-locked"><span class="badge-dot dot-locked"></span>Đã khóa</span>`;
      }

      // Role Badge
      const roleBadge = u.role === 'admin' 
        ? `<span class="badge badge-role-admin">Admin</span>`
        : `<span class="badge badge-role-user">User</span>`;

      // Verified status
      const verifiedHtml = u.email_verified_at 
        ? `<span class="verified-icon" title="Xác thực lúc ${formatDate(u.email_verified_at)}">✓ Đã xác thực</span>`
        : `<span class="unverified-icon">✗ Chưa</span>`;

      html += `
        <tr data-user-id="${u.id}">
          <td>
            <div class="user-cell">
              ${avatarHtml}
              <div class="user-info-text">
                <span class="user-display-name">${escapeHtml(u.display_name)}</span>
                <span class="user-email-text">${escapeHtml(u.email)}</span>
              </div>
            </div>
          </td>
          <td><span style="font-family:var(--font-mono);">${escapeHtml(u.phone_number || '—')}</span></td>
          <td>${roleBadge}</td>
          <td>${statusBadge}</td>
          <td>${verifiedHtml}</td>
          <td><span style="color:var(--text-secondary); font-size:0.8rem;">${formatDate(u.created_at)}</span></td>
          <td class="text-right">
            <div class="action-buttons-wrap">
              <button class="btn-action btn-view-detail" data-id="${u.id}" title="Xem chi tiết hồ sơ">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path><circle cx="12" cy="12" r="3"></circle></svg>
                <span>Chi tiết</span>
              </button>
              <button class="btn-action btn-action-status btn-change-status" data-id="${u.id}" data-name="${escapeHtml(u.display_name)}" data-status="${u.status}" title="Đổi trạng thái">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"></path></svg>
                <span>Đổi TT</span>
              </button>
            </div>
          </td>
        </tr>
      `;
    });

    el.accountsTableBody.innerHTML = html;

    // Attach row events
    el.accountsTableBody.querySelectorAll('.btn-view-detail').forEach(b => {
      b.addEventListener('click', () => openAccountDetailModal(b.dataset.id));
    });

    el.accountsTableBody.querySelectorAll('.btn-change-status').forEach(b => {
      b.addEventListener('click', () => openUpdateStatusModal(b.dataset.id, b.dataset.name, b.dataset.status));
    });
  }

  function renderPagination() {
    const total = state.accounts.totalItems;
    const page = state.accounts.page;
    const limit = state.accounts.limit;
    const totalPages = state.accounts.totalPages || 1;

    const start = total === 0 ? 0 : (page - 1) * limit + 1;
    const end = Math.min(page * limit, total);

    el.pagStart.textContent = start;
    el.pagEnd.textContent = end;
    el.pagTotal.textContent = total;
    el.pagCurrent.textContent = `Trang ${page} / ${totalPages}`;

    el.btnPagPrev.disabled = page <= 1;
    el.btnPagNext.disabled = page >= totalPages;
  }

  // ==========================================
  // ACCOUNT DETAIL MODAL (AC-2)
  // ==========================================
  async function openAccountDetailModal(userId) {
    state.selectedUserId = userId;
    el.modalAccountDetail.style.display = 'flex';
    el.detailModalBody.innerHTML = `<div class="inline-spinner-wrap text-center" style="padding:3rem;"><div class="inline-spinner"></div></div>`;
    el.detailDisplayName.textContent = 'Đang tải thông tin...';
    el.detailEmail.textContent = '...';

    try {
      const res = await apiRequest(`/api/v1/admin/accounts/${userId}`);
      const d = res.data || {};
      state.selectedUserDetail = d;

      el.detailDisplayName.textContent = d.display_name || 'Người dùng';
      el.detailEmail.textContent = d.email || '';

      // Masked Bank Info
      const bank = d.bank || {};
      const bankCode = bank.bank_code || 'Chưa thiết lập';
      const bankHolder = bank.bank_account_holder || 'Chưa thiết lập';
      const bankAcc = bank.bank_account_number || 'Chưa thiết lập';

      // Financials
      const fin = d.financials || {};

      // Groups table
      const groups = d.groups || [];
      let groupsHtml = '<div style="color:var(--text-muted); font-size:0.85rem;">Chưa tham gia nhóm nào.</div>';
      if (groups.length > 0) {
        groupsHtml = `
          <div class="table-responsive" style="margin-top:0.5rem;">
            <table class="data-table" style="font-size:0.8rem;">
              <thead>
                <tr>
                  <th>Tên nhóm</th>
                  <th>Vai trò</th>
                  <th>Trạng thái</th>
                  <th>Ngày tham gia</th>
                </tr>
              </thead>
              <tbody>
                ${groups.map(g => `
                  <tr>
                    <td><strong>${escapeHtml(g.group_name)}</strong></td>
                    <td><span class="badge ${g.role === 'captain' ? 'badge-role-admin' : 'badge-role-user'}">${escapeHtml(g.role)}</span></td>
                    <td>${g.status === 'active' ? '<span class="badge badge-active">Active</span>' : '<span class="badge badge-locked">Inactive</span>'}</td>
                    <td>${formatDate(g.joined_at)}</td>
                  </tr>
                `).join('')}
              </tbody>
            </table>
          </div>
        `;
      }

      // Recent Audit Logs
      const auditLogs = d.recent_audit_logs || [];
      let auditHtml = '<div style="color:var(--text-muted); font-size:0.85rem;">Chưa có bản ghi kiểm toán nào cho tài khoản này.</div>';
      if (auditLogs.length > 0) {
        auditHtml = `
          <div class="table-responsive" style="margin-top:0.5rem;">
            <table class="data-table" style="font-size:0.8rem;">
              <thead>
                <tr>
                  <th>Hành động</th>
                  <th>Lý do</th>
                  <th>Admin thực hiện</th>
                  <th>Thời gian</th>
                </tr>
              </thead>
              <tbody>
                ${auditLogs.map(a => `
                  <tr>
                    <td><span class="badge ${a.action === 'reactivate' ? 'badge-active' : 'badge-locked'}">${escapeHtml(a.action)}</span></td>
                    <td><em>${escapeHtml(a.reason)}</em></td>
                    <td><span style="font-family:var(--font-mono); font-size:0.75rem;">${escapeHtml(a.admin_email)}</span></td>
                    <td>${formatDate(a.created_at)}</td>
                  </tr>
                `).join('')}
              </tbody>
            </table>
          </div>
        `;
      }

      el.detailModalBody.innerHTML = `
        <div class="detail-grid">
          <!-- User Profile Block -->
          <div class="detail-block">
            <div class="detail-block-title">👤 Thông tin tài khoản</div>
            <div class="detail-item">
              <span class="detail-label">Mã ID:</span>
              <span class="detail-val" style="font-size:0.7rem;">${d.id}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">Số điện thoại:</span>
              <span class="detail-val">${escapeHtml(d.phone_number || '—')}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">Vai trò:</span>
              <span class="detail-val">${d.role}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">Trạng thái:</span>
              <span class="detail-val">${d.status}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">Ngày đăng ký:</span>
              <span class="detail-val">${formatDate(d.created_at)}</span>
            </div>
          </div>

          <!-- Security & Sessions Block -->
          <div class="detail-block">
            <div class="detail-block-title">🔒 Bảo mật & Phiên hoạt động</div>
            <div class="detail-item">
              <span class="detail-label">Phiên đang mở:</span>
              <span class="detail-val" style="color:#818CF8;">${d.active_sessions_count || 0} session</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">Số lần đăng nhập sai:</span>
              <span class="detail-val" style="color:${(d.failed_login_count || 0) > 0 ? 'var(--color-danger)' : 'inherit'};">${d.failed_login_count || 0}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">Khóa đăng nhập đến:</span>
              <span class="detail-val">${d.login_blocked_until ? formatDate(d.login_blocked_until) : 'Không'}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">Xác thực Email:</span>
              <span class="detail-val">${d.email_verified_at ? formatDate(d.email_verified_at) : 'Chưa'}</span>
            </div>
          </div>

          <!-- Bank Account Block -->
          <div class="detail-block">
            <div class="detail-block-title">🏦 Ngân hàng mặc định (Masked)</div>
            <div class="detail-item">
              <span class="detail-label">Mã ngân hàng:</span>
              <span class="detail-val">${escapeHtml(bankCode)}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">Số tài khoản:</span>
              <span class="detail-val" style="color:#34D399;">${escapeHtml(bankAcc)}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">Chủ tài khoản:</span>
              <span class="detail-val">${escapeHtml(bankHolder)}</span>
            </div>
          </div>

          <!-- Financial Summary Block -->
          <div class="detail-block">
            <div class="detail-block-title">💰 Công nợ trong các nhóm</div>
            <div class="detail-item">
              <span class="detail-label">Nợ chưa trả:</span>
              <span class="detail-val" style="color:var(--color-danger);">${fin.outstanding_debts_count || 0} khoản (${formatMoneyVND(fin.total_debt_amount_vnd)})</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">Phải thu chưa nhận:</span>
              <span class="detail-val" style="color:var(--color-success);">${fin.outstanding_credits_count || 0} khoản (${formatMoneyVND(fin.total_credit_amount_vnd)})</span>
            </div>
          </div>
        </div>

        <!-- Groups Section -->
        <div class="detail-section-title">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"></path><circle cx="9" cy="7" r="4"></circle></svg>
          <span>Danh sách nhóm tham gia (${groups.length})</span>
        </div>
        ${groupsHtml}

        <!-- Audit Logs Section -->
        <div class="detail-section-title" style="margin-top:1.5rem;">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline><line x1="16" y1="13" x2="8" y2="13"></line><line x1="16" y1="17" x2="8" y2="17"></line></svg>
          <span>Nhật ký kiểm toán Admin gần nhất (10 bản ghi)</span>
        </div>
        ${auditHtml}
      `;
    } catch (err) {
      if (err.status !== 401) {
        showToast('Không thể tải chi tiết: ' + err.message, 'error');
        el.detailModalBody.innerHTML = `<div style="color:var(--color-danger); text-align:center; padding:2rem;">Lỗi: ${escapeHtml(err.message)}</div>`;
      }
    }
  }

  // ==========================================
  // UPDATE STATUS MODAL (AC-3, AC-4)
  // ==========================================
  function openUpdateStatusModal(userId, userName, currentStatus) {
    state.selectedUserId = userId;
    el.statusTargetUserId.value = userId;
    el.statusTargetUserName.textContent = `${userName} (Trạng thái hiện tại: ${currentStatus})`;
    el.selectNewStatus.value = currentStatus === 'active' ? 'suspended' : 'active';
    el.inputStatusReason.value = '';
    
    updateReasonRequirement();
    el.modalUpdateStatus.style.display = 'flex';
  }

  function updateReasonRequirement() {
    const st = el.selectNewStatus.value;
    if (st === 'suspended' || st === 'locked') {
      el.inputStatusReason.required = true;
      el.reasonRequiredStar.style.display = 'inline';
    } else {
      el.inputStatusReason.required = false;
      el.reasonRequiredStar.style.display = 'none';
    }
  }

  async function handleUpdateStatusSubmit(e) {
    e.preventDefault();
    const userId = el.statusTargetUserId.value;
    const status = el.selectNewStatus.value;
    const reason = el.inputStatusReason.value.trim();

    if ((status === 'suspended' || status === 'locked') && !reason) {
      showToast('Vui lòng nhập lý do điều chỉnh trạng thái!', 'warning');
      return;
    }

    const btnText = el.btnSubmitStatus.querySelector('.btn-text');
    const spinner = el.btnSubmitStatus.querySelector('.spinner');
    btnText.style.display = 'none';
    spinner.style.display = 'inline-block';
    el.btnSubmitStatus.disabled = true;

    try {
      const res = await apiRequest(`/api/v1/admin/accounts/${userId}/status`, {
        method: 'PUT',
        body: JSON.stringify({ status, reason })
      });

      const data = res.data || {};
      const warning = data.warning || {};

      let warningMsg = '';
      if (warning.unsettled_debts_count > 0 || warning.unsettled_credits_count > 0) {
        warningMsg = ` (Cảnh báo: Tài khoản vẫn còn ${warning.unsettled_debts_count} khoản nợ và ${warning.unsettled_credits_count} khoản có chưa tất toán)`;
      }

      showToast(`Cập nhật trạng thái người dùng thành công!${warningMsg}`, 'success', 6000);
      el.modalUpdateStatus.style.display = 'none';

      // Reload accounts table and detail if open
      loadAccounts();
      if (el.modalAccountDetail.style.display === 'flex') {
        openAccountDetailModal(userId);
      }
    } catch (err) {
      showToast(err.message || 'Cập nhật trạng thái thất bại', 'error');
    } finally {
      btnText.style.display = 'inline-block';
      spinner.style.display = 'none';
      el.btnSubmitStatus.disabled = false;
    }
  }

  // ==========================================
  // EVENT BINDINGS
  // ==========================================
  function bindEvents() {
    // Auth
    el.loginForm.addEventListener('submit', handleLogin);
    el.btnLogout.addEventListener('click', handleLogout);

    // Tab Navigation
    el.navItems.forEach(item => {
      item.addEventListener('click', () => switchTab(item.dataset.tab));
    });

    // Overview controls
    el.btnRefreshOverview.addEventListener('click', () => {
      loadSystemOverview();
      checkHealthStatus();
      showToast('Đã làm mới tổng quan hệ thống', 'info');
    });

    el.toggleAutoRefresh.addEventListener('change', startAutoRefresh);

    // Accounts Filters
    el.btnRefreshAccounts.addEventListener('click', () => {
      loadAccounts();
      showToast('Đã tải lại danh sách tài khoản', 'info');
    });

    let searchTimeout;
    el.filterSearch.addEventListener('input', () => {
      clearTimeout(searchTimeout);
      const val = el.filterSearch.value.trim();
      el.btnClearSearch.style.display = val ? 'block' : 'none';
      searchTimeout = setTimeout(() => {
        state.accounts.search = val;
        state.accounts.page = 1;
        loadAccounts();
      }, 400);
    });

    el.btnClearSearch.addEventListener('click', () => {
      el.filterSearch.value = '';
      el.btnClearSearch.style.display = 'none';
      state.accounts.search = '';
      state.accounts.page = 1;
      loadAccounts();
    });

    el.filterStatus.addEventListener('change', () => {
      state.accounts.status = el.filterStatus.value;
      state.accounts.page = 1;
      loadAccounts();
    });

    el.filterRole.addEventListener('change', () => {
      state.accounts.role = el.filterRole.value;
      state.accounts.page = 1;
      loadAccounts();
    });

    el.filterSortBy.addEventListener('change', () => {
      state.accounts.sortBy = el.filterSortBy.value;
      loadAccounts();
    });

    el.filterSortOrder.addEventListener('change', () => {
      state.accounts.sortOrder = el.filterSortOrder.value;
      loadAccounts();
    });

    // Pagination
    el.pagLimit.addEventListener('change', () => {
      state.accounts.limit = parseInt(el.pagLimit.value, 10) || 20;
      state.accounts.page = 1;
      loadAccounts();
    });

    el.btnPagPrev.addEventListener('click', () => {
      if (state.accounts.page > 1) {
        state.accounts.page--;
        loadAccounts();
      }
    });

    el.btnPagNext.addEventListener('click', () => {
      if (state.accounts.page < state.accounts.totalPages) {
        state.accounts.page++;
        loadAccounts();
      }
    });

    // Modals Close Events
    el.btnCloseDetail.addEventListener('click', () => el.modalAccountDetail.style.display = 'none');
    el.btnCloseDetailFooter.addEventListener('click', () => el.modalAccountDetail.style.display = 'none');

    el.btnOpenStatusFromDetail.addEventListener('click', () => {
      if (state.selectedUserDetail) {
        openUpdateStatusModal(state.selectedUserDetail.id, state.selectedUserDetail.display_name, state.selectedUserDetail.status);
      }
    });

    el.btnCloseStatus.addEventListener('click', () => el.modalUpdateStatus.style.display = 'none');
    el.btnCloseStatusFooter.addEventListener('click', () => el.modalUpdateStatus.style.display = 'none');
    el.selectNewStatus.addEventListener('change', updateReasonRequirement);
    el.formUpdateStatus.addEventListener('submit', handleUpdateStatusSubmit);

    // API Config Modal
    el.btnApiConfig.addEventListener('click', () => el.modalApiConfig.style.display = 'flex');
    el.btnCloseApiConfig.addEventListener('click', () => el.modalApiConfig.style.display = 'none');
    el.btnCloseApiConfigFooter.addEventListener('click', () => el.modalApiConfig.style.display = 'none');
    el.btnSaveApiConfig.addEventListener('click', () => {
      const url = el.cfgApiUrl.value.trim();
      if (url) {
        state.apiBaseUrl = url;
        localStorage.setItem('paysplit_admin_api_url', url);
        el.modalApiConfig.style.display = 'none';
        showToast('Đã lưu cấu hình API: ' + url, 'success');
        checkHealthStatus();
      }
    });

    // Close modals when clicking overlay backdrop
    window.addEventListener('click', (e) => {
      if (e.target === el.modalAccountDetail) el.modalAccountDetail.style.display = 'none';
      if (e.target === el.modalUpdateStatus) el.modalUpdateStatus.style.display = 'none';
      if (e.target === el.modalApiConfig) el.modalApiConfig.style.display = 'none';
    });
  }

  // ==========================================
  // PROBES TESTING (AC-6, AC-8)
  // ==========================================
  window.testProbe = async function (path) {
    const box = document.getElementById('probe-output-box');
    const title = document.getElementById('probe-output-title');
    const content = document.getElementById('probe-output-content');

    box.style.display = 'block';
    title.textContent = `GET ${path} (Đang gọi...)`;
    content.textContent = 'Loading...';

    try {
      const url = `${state.apiBaseUrl.replace(/\/$/, '')}${path}`;
      const t0 = performance.now();
      const res = await fetch(url);
      const t1 = performance.now();
      const status = res.status;
      const text = await res.text();

      title.textContent = `GET ${path} — HTTP ${status} (${(t1 - t0).toFixed(1)}ms)`;
      try {
        const json = JSON.parse(text);
        content.textContent = JSON.stringify(json, null, 2);
      } catch {
        content.textContent = text;
      }
    } catch (err) {
      title.textContent = `GET ${path} — Lỗi`;
      content.textContent = err.message;
    }
  };

  window.openMetrics = function () {
    const url = `${state.apiBaseUrl.replace(/\/$/, '')}/metrics`;
    window.open(url, '_blank');
  };

  // Run app
  init();
})();
