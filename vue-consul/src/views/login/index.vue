<template>
  <div class="login-container">
    <main class="login-panel" aria-labelledby="login-title">
      <div class="brand-lockup">
        <span class="brand-mark" aria-hidden="true">T</span>
        <div><h1 id="login-title">TenSunS</h1><p>云资源同步控制台</p></div>
      </div>
    <el-form ref="loginForm" :model="loginForm" :rules="loginRules" class="login-form" auto-complete="on" label-position="left">
      <el-form-item prop="username">
        <span class="svg-container">
          <svg-icon icon-class="user" />
        </span>
        <el-input
          ref="username"
          v-model="loginForm.username"
          placeholder="用户名"
          name="username"
          type="text"
          tabindex="1"
          auto-complete="on"
        />
      </el-form-item>

      <el-form-item prop="password">
        <span class="svg-container">
          <svg-icon icon-class="password" />
        </span>
        <el-input
          :key="passwordType"
          ref="password"
          v-model="loginForm.password"
          :type="passwordType"
          placeholder="密码"
          name="password"
          tabindex="2"
          auto-complete="on"
          @keyup.enter.native="handleLogin"
        />
        <span class="show-pwd" @click="showPwd">
          <svg-icon :icon-class="passwordType === 'password' ? 'eye' : 'eye-open'" />
        </span>
      </el-form-item>
      <el-button :loading="loading" type="primary" class="login-button" @click.native.prevent="handleLogin">登录</el-button>
    </el-form>
      <p class="version">{{ VER }}</p>
    </main>
  </div>
</template>

<script>
export default {
  name: 'Login',
  data() {
    const validateUsername = (rule, value, callback) => {
      if (!value) {
        callback(new Error('请输入用户名'))
      } else {
        callback()
      }
    }
    const validatePassword = (rule, value, callback) => {
      if (value.length < 6) {
        callback(new Error('密码不能少于6位'))
      } else {
        callback()
      }
    }
    return {
      loginForm: {
        username: 'admin',
        password: ''
      },
      loginRules: {
        username: [{ required: true, trigger: 'blur', validator: validateUsername }],
        password: [{ required: true, trigger: 'blur', validator: validatePassword }]
      },
      loading: false,
      passwordType: 'password',
      redirect: undefined
    }
  },
  watch: {
    $route: {
      handler: function(route) {
        this.redirect = route.query && route.query.redirect
      },
      immediate: true
    }
  },
  methods: {
    showPwd() {
      if (this.passwordType === 'password') {
        this.passwordType = ''
      } else {
        this.passwordType = 'password'
      }
      this.$nextTick(() => {
        this.$refs.password.focus()
      })
    },
    handleLogin() {
      this.$refs.loginForm.validate(valid => {
        if (valid) {
          this.loading = true
          this.$store.dispatch('user/login', this.loginForm).then(() => {
            this.$router.push({ path: this.redirect || '/' })
            this.loading = false
          }).catch(() => {
            this.loading = false
          })
        } else {
          console.log('error submit!!')
          return false
        }
      })
    }
  }
}
</script>

<style lang="scss">
/* 修复input 背景不协调 和光标变色 */
/* Detail see https://github.com/PanJiaChen/vue-element-admin/pull/927 */

$bg:#f7fcfa;
$light_gray:#16333a;
$cursor: #16333a;

@supports (-webkit-mask: none) and (not (cater-color: $cursor)) {
  .login-container .el-input input {
    color: $cursor;
  }
}

/* reset element-ui css */
.login-container {
  .el-input {
    display: inline-block;
    height: 47px;
    width: 85%;

    input {
      background: #f7fcfa;
      border: 1px solid #b6cdc6;
      -webkit-appearance: none;
      border-radius: 0px;
      padding: 12px 5px 12px 15px;
      color: $light_gray;
      height: 47px;
      caret-color: $cursor;

      &:-webkit-autofill {
        box-shadow: 0 0 0px 1000px $bg inset !important;
        -webkit-text-fill-color: $cursor !important;
      }
    }
  }

  .login-button {
    width:100%;
    display: block;
    margin: 0 auto;
    margin-bottom:15px;
    border: 0;
    background: #117a78;
  }
  .el-form-item {
    border: 0;
    background: transparent;
    border-radius: 0;
    color: #454545;
  }
  .el-form-item__content {
    line-height: 33px;
    position: relative;
    font-size: 14px
  }
}
</style>

<style lang="scss" scoped>
$bg:#12303a;
$dark_gray:#49716d;
$light_gray:#f7fcfa;

.login-container {
  width: 100%;
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 24px;
  background: radial-gradient(circle at 18% 20%, rgba(224, 164, 88, .2), transparent 29%), linear-gradient(135deg, #0c242d 0%, #123f47 57%, #176c68 100%);

  .login-panel {
    width: 430px;
    max-width: 100%;
    padding: 40px;
    box-sizing: border-box;
    background: rgba(247, 252, 250, .97);
    border: 1px solid rgba(255, 247, 231, .45);
    border-top: 6px solid #e0a458;
    box-shadow: 18px 18px 0 rgba(7, 27, 34, .22);
  }

  .brand-lockup { display: flex; align-items: center; gap: 14px; margin-bottom: 32px; }
  .brand-lockup h1 { margin: 0; color: #12303a; font: 700 29px/1 Georgia, "Songti SC", serif; letter-spacing: .04em; }
  .brand-lockup p { margin: 7px 0 0; color: #49716d; font-size: 13px; }
  .brand-mark { display: grid; width: 48px; height: 48px; place-items: center; background: #12303a; color: #fff7e7; font: 700 24px/1 Georgia, serif; }

  .login-form {
    width: 100%;
    max-width: 100%;
  }

  .tips {
    font-size: 14px;
    color: #fff;
    margin-bottom: 10px;

    span {
      &:first-of-type {
        margin-right: 16px;
      }
    }
  }

  .svg-container {
    padding: 6px 5px 6px 15px;
    color: $dark_gray;
    vertical-align: middle;
    width: 30px;
    display: inline-block;
  }

  .version { margin: 20px 0 0; color: #49716d; font-size: 12px; text-align: center; }
  .show-pwd {
    position: absolute;
    right: 10px;
    top: 7px;
    font-size: 16px;
    color: $dark_gray;
    cursor: pointer;
    user-select: none;
  }
}
</style>
